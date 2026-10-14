package rex

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdk "github.com/helpin-ai/agent-runtime-go"
)

// Config holds the host's process-level settings.
type Config struct {
	DataDir       string
	WorkspaceRoot string
	// HostURL is how the runtime reaches this host (callbacks, MCP).
	HostURL string
	// InternalToken authenticates runtime → host callbacks.
	InternalToken string
	// UIToken authenticates the desktop webview → host API.
	UIToken string
	// MCPSecret derives stable per-bank bearer tokens for the memory MCP
	// server. It must survive restarts because paused runs keep their token.
	MCPSecret []byte
}

// ErrWriteToolsUnavailable explains why write and shell access are off.
var ErrWriteToolsUnavailable = errors.New("writing files and running commands need the macOS sandbox (Seatbelt), which is not active in the bot engine. This bot can read files in its folder")

// Host coordinates bots, conversations, the runtime, memory and routines.
type Host struct {
	cfg      Config
	Store    *Store
	Runtime  *RuntimeClient
	Settings *SettingsManager
	Memory   *MemoryService
	Skills   *SkillLibrary
	Routines *Scheduler
	Browser  *BrowserControl

	runtimeReady atomic.Bool
	// writeTools is true once the runtime reports that agent commands are
	// confined with Seatbelt; only then are file writing and shell offered.
	writeTools   atomic.Bool
	restart      func()
	lastActivity atomic.Int64

	watchMu sync.Mutex
	watched map[string]bool
	wake    chan struct{}
	// retroWake asks the retrospective worker to look now.
	retroWake chan struct{}

	// detachAfter marks bots that reconfigured themselves; their next
	// message starts a new run with the new instructions and tools.
	detachMu    sync.Mutex
	detachAfter map[string]bool

	oauth mcpOAuth
	// github reads skill sources for the catalog (catalog.go).
	github *githubClient
}

func NewHost(cfg Config, store *Store, runtime *RuntimeClient, settings *SettingsManager, memory *MemoryService, skills *SkillLibrary) *Host {
	h := &Host{cfg: cfg, Store: store, Runtime: runtime, Settings: settings, Memory: memory, Skills: skills, watched: map[string]bool{}, wake: make(chan struct{}, 1), retroWake: make(chan struct{}, 1), github: newGitHubClient(), detachAfter: map[string]bool{}}
	h.Routines = NewScheduler(h)
	if memory != nil {
		memory.attach(h)
	}
	storedPasswords = func() []string { return h.KnownPasswords(context.Background()) }
	h.Browser = NewBrowserControl(h)
	h.touch()
	return h
}

func (h *Host) Config() Config { return h.cfg }

func (h *Host) SetRestart(fn func())       { h.restart = fn }
func (h *Host) SetRuntimeReady(ready bool) { h.runtimeReady.Store(ready) }
func (h *Host) RuntimeReady() bool         { return h.runtimeReady.Load() }
func (h *Host) touch()                     { h.lastActivity.Store(time.Now().UnixNano()) }
func (h *Host) IdleFor() time.Duration     { return time.Since(time.Unix(0, h.lastActivity.Load())) }
func (h *Host) RequestRuntimeRestart() {
	if h.restart != nil {
		h.restart()
	}
}

// Bootstrap runs once the runtime is ready: it seeds first-run bots, syncs
// every bot definition into the runtime and resumes watching open turns.
func (h *Host) Bootstrap(ctx context.Context) error {
	if capabilities, err := h.Runtime.GetCapabilities(ctx); err == nil {
		isolation := capabilities.ExecutionIsolation
		h.writeTools.Store(isolation == "seatbelt")
		slog.Info("bot engine command isolation", "isolation", isolation, "write_tools", h.WriteTools())
	}
	bots, err := h.Store.ListBots(ctx)
	if err != nil {
		return err
	}
	if len(bots) == 0 {
		settings, _ := h.Settings.Get(ctx)
		for _, bot := range DefaultBots(h.cfg.WorkspaceRoot, settings.DefaultProvider, settings.DefaultModel) {
			bot := bot
			if !h.WriteTools() {
				bot.Capabilities.FilesWrite, bot.Capabilities.Shell = false, false
			}
			if err := bot.Normalize(h.cfg.WorkspaceRoot); err != nil {
				return err
			}
			if err := h.Store.SaveBot(ctx, &bot); err != nil {
				return err
			}
		}
		bots, _ = h.Store.ListBots(ctx)
	}
	for _, bot := range bots {
		if err := h.SyncBot(ctx, bot); err != nil {
			slog.Error("could not sync bot to runtime", "bot", bot.ID, "error", err)
		}
	}
	if err := h.EnsureMainBot(ctx); err != nil {
		slog.Error("could not set up the main bot", "error", err)
	}
	if err := h.Store.MergeThreads(ctx, h.cfg.DataDir); err != nil {
		slog.Error("could not give each bot one conversation", "error", err)
	}
	h.recoverTurns(ctx)
	return nil
}

// PrepareInterruptedRuns runs before the agent runtime starts. The runtime
// continues runs that were cut off when it last stopped, so each such bot
// gets its own browser back first, on the page it was using. Browsers left
// from before are closed so a stale one never holds a bot's profile.
func (h *Host) PrepareInterruptedRuns(ctx context.Context) {
	h.Browser.CloseAll(ctx)
	turns, err := h.Store.WorkingTurns(ctx)
	if err != nil {
		return
	}
	for _, turn := range turns {
		if turn.RunID == "" {
			continue
		}
		if bot, err := h.Store.GetBot(ctx, turn.BotID); err == nil {
			h.Browser.Prepare(ctx, *bot, turn.RunID)
		}
	}
}

// SyncBot writes the bot's current definition into the runtime registry.
// botFingerprint summarises everything a run fixes when it starts: the
// system prompt (instructions, rules, skills), tools, model and MCP servers.
func (h *Host) botFingerprint(ctx context.Context, bot Bot) string {
	hash := sha256.New()
	settings, _ := h.Settings.Get(ctx)
	bot.BrowserBackend = bot.browserMode(settings)
	agent := bot.RuntimeAgent(bot.SystemPrompt(h.Skills.Resolve(bot.Skills)))
	fmt.Fprintln(hash, bot.BrowserBackend, agent.SystemPrompt, strings.Join(agent.AllowedTools, ","), agent.Provider, agent.Model, agent.ApprovalMode, strings.Join(bot.Skills, ","), bot.ContextWindow)
	for _, skill := range h.Skills.Resolve(bot.Skills) {
		fmt.Fprintln(hash, skill.ID, skill.Version)
	}
	if servers, err := h.runMCPServers(ctx, bot); err == nil {
		for _, server := range servers {
			names := make([]string, 0, len(server.Tools))
			for _, tool := range server.Tools {
				names = append(names, tool.Name+":"+tool.Access)
			}
			fmt.Fprintln(hash, server.ServerID, server.URL, strings.Join(names, ","))
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (h *Host) SyncBot(ctx context.Context, bot Bot) error {
	settings, err := h.Settings.Get(ctx)
	if err != nil {
		return err
	}
	bot.BrowserBackend = bot.browserMode(settings)
	skills := h.Skills.Resolve(bot.Skills)
	_, err = h.Runtime.UpsertAgent(ctx, bot.RuntimeAgent(bot.SystemPrompt(skills)))
	return err
}

// SaveBot validates, persists and syncs a bot, creating its workspace.
func (h *Host) SaveBot(ctx context.Context, bot *Bot) error {
	if err := bot.Normalize(h.cfg.WorkspaceRoot); err != nil {
		return err
	}
	if !h.WriteTools() && (bot.Capabilities.FilesWrite || bot.Capabilities.Shell) {
		return ErrWriteToolsUnavailable
	}
	if existing, err := h.Store.GetBot(ctx, bot.ID); err == nil {
		if bot.BrowserBackend != existing.BrowserBackend || (existing.Capabilities.Browser && !bot.Capabilities.Browser) {
			if h.Browser.Held(bot.ID) {
				return errors.New("hand back the browser before changing its mode")
			}
			h.Browser.mu.Lock()
			runID := h.Browser.active[bot.ID]
			h.Browser.mu.Unlock()
			if runID != "" {
				run, err := h.Runtime.GetRun(ctx, runID)
				if err != nil {
					return errors.New("could not check the current browser task")
				}
				if run.Status != "completed" && run.Status != "failed" && run.Status != "cancelled" {
					return errors.New("stop the current task before changing browser mode")
				}
			}
			settings, err := h.Settings.Get(ctx)
			if err != nil {
				return err
			}
			if existing.Capabilities.Browser && (existing.browserMode(settings) != bot.browserMode(settings) || !bot.Capabilities.Browser) {
				if existing.browserMode(settings) == "extension" {
					status, err := h.Browser.extensionControl(ctx, bot.ID, "status")
					if err != nil {
						return fmt.Errorf("could not check browser before changing mode: %w", err)
					}
					if status.Held {
						return errors.New("hand back the browser before changing its mode")
					}
					if status.RunID != "" {
						run, err := h.Runtime.GetRun(ctx, status.RunID)
						if err != nil || !run.Terminal() {
							return errors.New("stop the current task before changing browser mode")
						}
					}
					if _, err := h.Browser.extensionControl(ctx, bot.ID, "close"); err != nil {
						return err
					}
				} else {
					h.Browser.closeBotSessions(ctx, bot.ID, "")
				}
			}
		}
		bot.CreatedAt = existing.CreatedAt
	}
	if bot.UsesWorkspace() {
		if err := ensureWorkspace(bot.WorkspaceDir); err != nil {
			return err
		}
	}
	if err := h.Store.SaveBot(ctx, bot); err != nil {
		return err
	}
	// Memory follows the bots' chat models; pick up a model change.
	_ = h.Memory.Configure(ctx)
	if h.RuntimeReady() {
		return h.SyncBot(ctx, *bot)
	}
	return nil
}

func ensureWorkspace(root string) error {
	for _, dir := range []string{root, filepath.Join(root, "inbox"), filepath.Join(root, "outputs")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create workspace folder: %w", err)
		}
	}
	return nil
}

// ErrBusy means the bot is still working on the previous message.
var ErrBusy = errors.New("the bot is still working on the previous message")

// SendMessage delivers a user message to a conversation. It resumes the
// conversation's paused run when possible and otherwise starts a new run that
// carries the recent transcript forward.
func (h *Host) SendMessage(ctx context.Context, conversationID, content string) (*Run, *Turn, error) {
	content = strings.TrimSpace(content)
	return h.deliver(ctx, conversationID, delivery{UserText: content, ModelText: content})
}

// EditLastMessage replaces the conversation's latest message with new text.
// The old exchange is removed with anything remembered from it, and the bot
// answers the new text in a new run that carries the conversation up to,
// not including, the old message.
func (h *Host) EditLastMessage(ctx context.Context, conversationID, turnID, content string) (*Run, *Turn, error) {
	if strings.TrimSpace(content) == "" {
		return nil, nil, errors.New("message content is required")
	}
	conversation, err := h.Store.GetConversation(ctx, conversationID)
	if err != nil {
		return nil, nil, err
	}
	latest, err := h.Store.LatestTurnForConversation(ctx, conversationID)
	if err != nil || latest.ID != turnID {
		return nil, nil, errors.New("only your latest message can be edited")
	}
	if latest.Origin != "" || strings.TrimSpace(latest.UserText) == "" {
		return nil, nil, errors.New("only messages you wrote can be edited")
	}
	if conversation.ActiveRunID != nil {
		if run, err := h.Runtime.GetRun(ctx, *conversation.ActiveRunID); err == nil && !run.Terminal() {
			switch {
			case run.Status == "queued" || run.Status == "running":
				return nil, nil, ErrBusy
			case run.Status == "paused" && run.PauseReason != "awaiting_user_message":
				return nil, nil, errors.New("the bot is waiting for your approval or sign-in above; answer that first")
			}
			// The run holds the old exchange: close it, and carry the
			// conversation forward without it.
			_, _ = h.Runtime.CancelRun(ctx, run.ID)
		}
	}
	if latest.RunID != "" {
		if err := h.Store.AddContextCut(ctx, conversationID, latest.RunID, latest.StartedAt); err != nil {
			return nil, nil, err
		}
	}
	if err := h.Store.DetachRun(ctx, conversationID); err != nil {
		return nil, nil, err
	}
	if latest.MemoryBank != "" {
		_ = h.Memory.Forget(ctx, latest.MemoryBank, turnDocument(latest.ID))
	}
	if err := h.Store.DeleteTurn(ctx, latest.ID); err != nil {
		return nil, nil, err
	}
	return h.SendMessage(ctx, conversationID, content)
}

// delivery is a message for a bot. The user's messages are shown and sent
// as typed. Messages Rex writes itself (a task from the main bot, a
// report back to it) carry an origin and reach the bot as a system
// notification, never as the user's own words.
type delivery struct {
	UserText    string // shown in the transcript; empty shows only the bot
	ModelText   string // what the bot receives
	Origin      string
	DelegatedBy string
}

func (d delivery) fromRex() bool { return d.Origin != "" }

func (h *Host) deliver(ctx context.Context, conversationID string, d delivery) (*Run, *Turn, error) {
	content := strings.TrimSpace(d.ModelText)
	if content == "" {
		return nil, nil, errors.New("message content is required")
	}
	if !h.RuntimeReady() {
		return nil, nil, errors.New("the bot engine is still starting")
	}
	conversation, err := h.Store.GetConversation(ctx, conversationID)
	if err != nil {
		return nil, nil, err
	}
	bot, err := h.Store.GetBot(ctx, conversation.BotID)
	if err != nil {
		return nil, nil, err
	}
	h.touch()
	turn := &Turn{ConversationID: conversation.ID, BotID: bot.ID, UserText: d.UserText, MemoryBank: bot.MemoryBank(), Origin: d.Origin, DelegatedBy: d.DelegatedBy}
	if conversation.ContextStart != nil {
		if latest, err := h.Store.LatestTurnForConversation(ctx, conversation.ID); err == nil && latest.StartedAt.Before(*conversation.ContextStart) {
			turn.Section = FreshStartSection
		}
	}
	if turn.MemoryBank == "" {
		turn.MemoryStatus = "off"
	}

	if conversation.ActiveRunID != nil {
		run, err := h.Runtime.GetRun(ctx, *conversation.ActiveRunID)
		if err == nil {
			switch {
			case run.Status == "queued" || run.Status == "running":
				return nil, nil, ErrBusy
			case d.fromRex() && run.Status == "paused" && run.PauseReason != "awaiting_user_message":
				// Waiting on the user (a question or approval): only the
				// user can continue this bot now.
				return nil, nil, ErrBusy
			case run.Status == "paused" && run.PauseReason == "awaiting_user_message" && conversation.RunFingerprint != h.botFingerprint(ctx, *bot):
				// The bot's instructions, skills or tools changed since this
				// run started; a run keeps what it started with, so continue
				// the conversation in a new run below.
				slog.Info("bot setup changed; starting a new run", "bot", bot.ID, "run", run.ID)
			case run.Status == "paused" && (run.PauseReason == "awaiting_user_message" || run.PauseReason == "human_input" || run.PauseReason == "none"):
				turn.RunID = run.ID
				if latest, err := h.Runtime.LatestSequence(ctx, run.ID); err == nil {
					turn.StartSequence = latest
				}
				if err := h.Store.CreateTurn(ctx, turn); err != nil {
					return nil, nil, err
				}
				h.Browser.Prepare(ctx, *bot, run.ID)
				var resumed *Run
				if d.fromRex() {
					resumed, err = h.Runtime.NotifyRun(ctx, run.ID, content, "turn-"+turn.ID)
				} else {
					resumed, err = h.Runtime.ResumeRun(ctx, run.ID, "reply", content, "", "turn-"+turn.ID)
				}
				if err == nil {
					_ = h.Store.TouchConversation(ctx, conversation.ID)
					h.Watch(run.ID)
					return resumed, turn, nil
				}
				// The paused run cannot continue (for example its stored
				// credentials are gone). Close it and continue in a new run
				// that carries the conversation forward.
				slog.Warn("resume failed; starting a new run", "run", run.ID, "error", err)
				_ = h.Store.DeleteTurn(ctx, turn.ID)
				_, _ = h.Runtime.CancelRun(ctx, run.ID)
				turn.ID, turn.RunID, turn.StartSequence = "", "", 0
			case run.Status == "paused":
				return nil, nil, errors.New("the bot is waiting for your approval or sign-in above; answer that first")
			}
		}
	}

	if err := h.Store.CreateTurn(ctx, turn); err != nil {
		return nil, nil, err
	}
	run, err := h.startRun(ctx, *bot, conversation, h.instructionsWithHistory(ctx, conversation, content), true, "turn-"+turn.ID)
	if err != nil {
		_ = h.Store.DeleteTurn(ctx, turn.ID)
		return nil, nil, err
	}
	turn.RunID = run.ID
	if err := h.Store.SetTurnRun(ctx, turn.ID, run.ID); err != nil {
		return nil, nil, err
	}
	h.Watch(run.ID)
	return run, turn, nil
}

// instructionsWithHistory prefixes a message with the recent transcript when
// it starts a new run inside an existing conversation.
func (h *Host) instructionsWithHistory(ctx context.Context, conversation *Conversation, content string) string {
	runIDs, _ := h.Store.ContextRunIDs(ctx, conversation)
	if len(runIDs) == 0 {
		return content
	}
	messages, err := h.Runtime.ConversationTranscript(ctx, runIDs)
	if err != nil {
		return content
	}
	if cuts, err := h.Store.ContextCuts(ctx, conversation.ID); err == nil {
		messages = withoutCuts(messages, cuts)
	}
	if carry := carryForward(messages); carry != "" {
		// The transcript mixes in assistant and tool output, so it is marked
		// as previous conversation, which reviewers treat as untrusted.
		return "<previous_conversation>\n" + carry + "\n</previous_conversation>\n\nNew user message:\n" + content
	}
	return content
}

// trustedUserMessages lists what the user said earlier in a conversation, so
// a new run can show the reviewer the user's own words, not the transcript.
func (h *Host) trustedUserMessages(ctx context.Context, conversationID string) []string {
	turns, err := h.Store.TurnsForConversation(ctx, conversationID)
	if err != nil {
		return nil
	}
	messages := []string{}
	for _, turn := range turns {
		if text := strings.TrimSpace(turn.UserText); text != "" && turn.Origin == "" && turn.MemoryStatus != "forgotten" {
			messages = append(messages, text)
		}
	}
	if len(messages) > 20 {
		messages = messages[len(messages)-20:]
	}
	return messages
}

func (h *Host) startRun(ctx context.Context, bot Bot, conversation *Conversation, instructions string, chat bool, hostRunID string) (*Run, error) {
	if bot.Provider == ProviderLocal && h.Memory != nil {
		h.Memory.yieldToChat()
		h.yieldLocalReviews(ctx)
	}
	model, credential, err := h.Settings.RunModelFor(ctx, bot)
	if err != nil {
		return nil, err
	}
	if err := h.SyncBot(ctx, bot); err != nil {
		return nil, fmt.Errorf("sync bot: %w", err)
	}
	agent, resolved := h.runAgent(ctx, bot, model)
	if resolved.Enabled {
		instructions = boundCarriedContext(instructions, resolved.InputLimit/2)
	}
	if _, err := h.Runtime.UpsertAgent(ctx, agent); err != nil {
		return nil, fmt.Errorf("configure context policy: %w", err)
	}
	servers, err := h.runMCPServers(ctx, bot)
	if err != nil {
		return nil, err
	}
	run, err := h.Runtime.StartRun(ctx, StartRunSpec{
		AgentID: bot.ID, HostRunID: hostRunID, TargetType: "conversation", TargetID: conversation.ID,
		Instructions: instructions, Chat: chat, Model: model, ModelCredential: credential,
		Metadata:        map[string]any{"bot_id": bot.ID, "conversation_id": conversation.ID, "trusted_user_messages": h.trustedUserMessages(ctx, conversation.ID)},
		MCPServers:      servers,
		ExternalActorID: LocalActorID,
	})
	if err != nil {
		return nil, err
	}
	if err := h.Store.AttachRun(ctx, conversation.ID, run.ID); err != nil {
		return nil, err
	}
	_ = h.Store.SetRunFingerprint(ctx, conversation.ID, h.botFingerprint(ctx, bot))
	h.Browser.Prepare(ctx, bot, run.ID)
	return run, nil
}

// runMCPServers attaches Rex's memory tools and the bot's MCP connections.
func (h *Host) runMCPServers(ctx context.Context, bot Bot) ([]RunMCPServer, error) {
	var servers []RunMCPServer
	if bank := bot.MemoryBank(); bank != "" {
		servers = append(servers, RunMCPServer{
			ServerID: "rex-memory", ServerName: "memory", Transport: "streamable_http",
			URL:        h.cfg.HostURL + "/mcp/memory/" + base64.RawURLEncoding.EncodeToString([]byte(bank)),
			Tools:      []RunMCPTool{{Name: "recall", Access: "read"}, {Name: "remember", Access: "read"}, {Name: "history_search", Access: "read"}, {Name: "history_read", Access: "read"}},
			Credential: &RunMCPCredential{Type: "bearer_token", AccessToken: h.MemoryToken(bank)},
		})
	}
	if bot.Setup {
		servers = append(servers, h.setupMCPServer(bot))
	} else {
		servers = append(servers, h.selfMCPServer(bot))
	}
	if bot.Role == RoleMain {
		servers = append(servers, h.botsMCPServer(bot))
	}
	// Every bot can search the skill catalog and suggest a skill for
	// itself; the user installs it from the suggestion.
	servers = append(servers, h.catalogMCPServer(bot.ID, true))
	if bot.Capabilities.Browser && !bot.Setup {
		servers = append(servers, h.loginsMCPServer(bot))
	}
	connections, err := h.MCPServersForBot(ctx, bot)
	if err != nil {
		return nil, err
	}
	return append(servers, connections...), nil
}

// MemoryToken is the stable bearer token that grants one memory bank.
func (h *Host) MemoryToken(bank string) string {
	mac := hmac.New(sha256.New, h.cfg.MCPSecret)
	mac.Write([]byte("memory-bank:" + bank))
	return hex.EncodeToString(mac.Sum(nil))
}

// Respond answers a pending approval or question on a run.
func (h *Host) Respond(ctx context.Context, runID, interactionID, intent, content string) (*Run, error) {
	h.touch()
	resumeID := "interaction-" + interactionID
	if interactionID == "" {
		resumeID = "reply-" + randomHex(8)
	}
	if conversation, err := h.Store.ConversationForRun(ctx, runID); err == nil {
		if bot, err := h.Store.GetBot(ctx, conversation.BotID); err == nil {
			h.Browser.Prepare(ctx, *bot, runID)
		}
	}
	run, err := h.Runtime.ResumeRun(ctx, runID, intent, content, interactionID, resumeID)
	if err != nil {
		return nil, err
	}
	h.Watch(runID)
	return run, nil
}

func (h *Host) Cancel(ctx context.Context, runID string) (*Run, error) {
	h.touch()
	run, err := h.Runtime.CancelRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	h.Watch(runID)
	return run, nil
}

// --- run watching ---

// Watch follows a run until its current turn settles.
func (h *Host) Watch(runID string) {
	h.watchMu.Lock()
	h.watched[runID] = true
	h.watchMu.Unlock()
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

func (h *Host) ActiveRuns() int {
	h.watchMu.Lock()
	defer h.watchMu.Unlock()
	return len(h.watched)
}

// RunWatcher polls watched runs. Polling the local runtime is cheap and is
// more robust across runtime restarts than holding one SSE stream per run.
func (h *Host) RunWatcher(ctx context.Context) {
	ticker := time.NewTicker(700 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-h.wake:
		}
		if !h.RuntimeReady() {
			continue
		}
		h.watchMu.Lock()
		ids := make([]string, 0, len(h.watched))
		for id := range h.watched {
			ids = append(ids, id)
		}
		h.watchMu.Unlock()
		for _, id := range ids {
			done := h.checkRun(ctx, id)
			if done {
				h.watchMu.Lock()
				delete(h.watched, id)
				h.watchMu.Unlock()
			}
		}
	}
}

// checkRun settles the run's open turn when the run stops working. It
// returns true when the run no longer needs watching.
func (h *Host) checkRun(ctx context.Context, runID string) bool {
	run, err := h.Runtime.GetRun(ctx, runID)
	if err != nil {
		var runtimeErr *sdk.HTTPStatusError
		if errors.As(err, &runtimeErr) && runtimeErr.StatusCode == 404 {
			return true
		}
		return false
	}
	if run.Status == "queued" || run.Status == "running" {
		h.touch()
		return false
	}
	if run.Status == "paused" && run.PauseReason == "authentication" {
		// An MCP token expired or was rejected: refresh it and continue. If
		// the user must sign in again, the turn waits for them.
		return !h.resumeAuthPaused(ctx, run)
	}
	if run.Status == "paused" && run.PauseReason == "human_approval" {
		// Waiting on the user; the turn is still open.
		return true
	}
	status := "settled"
	switch run.Status {
	case "failed":
		if h.retryTransientFailure(ctx, run) {
			return true
		}
		status = "failed"
	case "cancelled":
		status = "cancelled"
	}
	h.settle(ctx, run, status)
	h.Routines.RunSettled(ctx, run)
	return true
}

var transientModelErrors = []string{"stream chunk", "unexpected end of json", "connection refused", "connection reset", "eof", "status code: 502", "status code: 503", "status code: 504", "429", "too many requests", "timeout"}

// retryTransientFailure re-sends a chat message once in a fresh run when
// the model connection failed and no tool ran during the turn, so nothing
// the bot did can be repeated.
func (h *Host) retryTransientFailure(ctx context.Context, run *Run) bool {
	message := strings.ToLower(run.ErrorMessage)
	transient := false
	for _, marker := range transientModelErrors {
		if strings.Contains(message, marker) {
			transient = true
			break
		}
	}
	if !transient {
		return false
	}
	if _, err := h.Store.RoutineRunByRunID(ctx, run.ID); err == nil {
		return false // routines have their own bounded retries
	}
	turn, err := h.Store.OpenTurnForRun(ctx, run.ID)
	if err != nil {
		return false
	}
	if attempts, err := h.Store.TurnAttempts(ctx, turn.ID); err != nil || attempts > 0 {
		return false
	}
	calls, err := h.Runtime.ListToolCalls(ctx, run.ID)
	if err != nil {
		return false
	}
	for _, call := range calls {
		if !call.CreatedAt.Before(turn.StartedAt.Add(-time.Second)) {
			return false
		}
	}
	conversation, err := h.Store.GetConversation(ctx, turn.ConversationID)
	if err != nil {
		return false
	}
	bot, err := h.Store.GetBot(ctx, turn.BotID)
	if err != nil {
		return false
	}
	// Wait before retrying: a local server that is loading another model
	// (for example the embedding model) aborts streams until it is done.
	go func(turn Turn, bot Bot, conversation Conversation, failedRun string) {
		time.Sleep(5 * time.Second)
		ctx := context.Background()
		retry, err := h.startRun(ctx, bot, &conversation, h.instructionsWithHistory(ctx, &conversation, turn.UserText), true, "turn-"+turn.ID+"-retry")
		if err != nil {
			slog.Warn("could not retry after a model error", "turn", turn.ID, "error", err)
			h.Watch(failedRun)
			_, _ = h.Store.RetryTurn(ctx, turn.ID, failedRun)
			return
		}
		if ok, err := h.Store.RetryTurn(ctx, turn.ID, retry.ID); err != nil || !ok {
			_, _ = h.Runtime.CancelRun(ctx, retry.ID)
			return
		}
		slog.Info("retrying a message after a transient model error", "turn", turn.ID, "failed_run", failedRun, "run", retry.ID)
		h.Watch(retry.ID)
	}(*turn, *bot, *conversation, run.ID)
	return true
}

func (h *Host) settle(ctx context.Context, run *Run, status string) {
	turn, err := h.Store.OpenTurnForRun(ctx, run.ID)
	if err != nil {
		return
	}
	answer := ""
	if messages, err := h.Runtime.ListMessages(ctx, run.ID); err == nil {
		var parts []string
		for _, message := range messages {
			if message.Role != "assistant" || message.CreatedAt.Before(turn.StartedAt.Add(-time.Second)) {
				continue
			}
			if text := strings.TrimSpace(message.Content); text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 {
			answer = parts[len(parts)-1]
		}
	}
	if status == "failed" && answer == "" {
		answer = run.ErrorMessage
	}
	if err := h.Store.SettleTurn(ctx, turn.ID, status, answer); err != nil {
		slog.Error("settle turn", "turn", turn.ID, "error", err)
		return
	}
	turn.Status, turn.AssistantText = status, answer
	if err := h.Store.QueueEpisode(ctx, *turn); err != nil {
		slog.Error("capture task episode", "turn", turn.ID, "error", err)
	}
	h.Memory.Notify()
	_ = h.Store.TouchConversation(ctx, turn.ConversationID)
	h.Browser.CloseWhenIdle(turn.BotID, run.ID)
	h.detachIfConfigured(ctx, turn.BotID, turn.ConversationID)
	if turn.DelegatedBy != "" {
		h.reportToMain(*turn, status, answer)
	}
	go h.noteTaskForRetro(context.WithoutCancel(ctx), *turn, status, answer)
	if status == "settled" {
		h.Memory.Notify()
	}
}

// Recheck persisted turns even when a watcher was lost; transient API errors
// are never evidence that an active run has finished.
func (h *Host) reconcileWorkingTurns(ctx context.Context) {
	turns, err := h.Store.WorkingTurns(ctx)
	if err != nil {
		return
	}
	for _, turn := range turns {
		if turn.RunID == "" {
			continue
		}
		run, err := h.Runtime.GetRun(ctx, turn.RunID)
		if err != nil {
			var missing *sdk.HTTPStatusError
			if errors.As(err, &missing) && missing.StatusCode == 404 {
				if h.Store.SettleTurn(ctx, turn.ID, "failed", "The recorded runtime run no longer exists.") == nil {
					_ = h.Store.QueueEpisode(ctx, turn)
					h.Memory.Notify()
				}
			}
			continue
		}
		if run.Terminal() || run.Status == "paused" && run.PauseReason == "awaiting_user_message" {
			status := "settled"
			if run.Status == "failed" || run.Status == "cancelled" {
				status = run.Status
			}
			h.settle(ctx, run, status)
			h.watchMu.Lock()
			delete(h.watched, run.ID)
			h.watchMu.Unlock()
		} else if run.Status == "queued" || run.Status == "running" {
			h.Watch(run.ID)
		}
	}
}

// recoverTurns re-attaches to turns that were open when the host stopped.
// A turn whose run cannot be found is marked interrupted, never re-run.
func (h *Host) recoverTurns(ctx context.Context) {
	turns, err := h.Store.WorkingTurns(ctx)
	if err != nil {
		return
	}
	for _, turn := range turns {
		if turn.RunID == "" {
			_ = h.Store.SettleTurn(ctx, turn.ID, "failed", "Rex stopped before this message reached the bot. Send it again.")
			_ = h.Store.SetTurnMemory(ctx, turn.ID, "skipped", "interrupted", 0)
			continue
		}
		if _, err := h.Runtime.GetRun(ctx, turn.RunID); err != nil {
			var missing *sdk.HTTPStatusError
			if errors.As(err, &missing) && missing.StatusCode == 404 {
				_ = h.Store.SettleTurn(ctx, turn.ID, "failed", "This run was interrupted and could not be recovered.")
				_ = h.Store.SetTurnMemory(ctx, turn.ID, "skipped", "interrupted", 0)
			} else {
				h.Watch(turn.RunID)
			}
			continue
		}
		h.Watch(turn.RunID)
	}
}

// withoutCuts drops what a run said from an edited message on.
func withoutCuts(messages []Message, cuts map[string]time.Time) []Message {
	if len(cuts) == 0 {
		return messages
	}
	kept := make([]Message, 0, len(messages))
	for _, message := range messages {
		if after, ok := cuts[message.RunID]; ok && !message.CreatedAt.Before(after) {
			continue
		}
		kept = append(kept, message)
	}
	return kept
}

func carryForward(messages []Message) string {
	if len(messages) > 24 {
		messages = messages[len(messages)-24:]
	}
	var lines []string
	total := 0
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		content := strings.TrimSpace(message.Content)
		if content == "" || (message.Role != "user" && message.Role != "assistant") {
			continue
		}
		if len(content) > 1200 {
			content = content[:1200] + "…"
		}
		line := message.Role + ": " + content
		if total+len(line) > 9000 {
			break
		}
		lines = append([]string{line}, lines...)
		total += len(line)
	}
	return strings.Join(lines, "\n")
}

func randomHex(bytes int) string {
	value := make([]byte, bytes)
	_, _ = rand.Read(value)
	return hex.EncodeToString(value)
}

// WriteTools reports whether file writing and shell commands are offered:
// only when the bot engine confines commands with Seatbelt.
func (h *Host) WriteTools() bool { return h.writeTools.Load() }
