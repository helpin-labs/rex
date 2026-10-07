package rex

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"time"

	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

// A new bot sets itself up by talking to the user: it greets them, asks
// what it should focus on and a few follow-up questions, then configures
// its own name, icon and instructions. Turning on tools (browser, files,
// shell) or adding a routine always waits for the user's approval.

//go:embed bot_icons.txt
var botIconList string

// botIconIDs are the icons bots used before faces; the same list as
// src/icons.ts. Stored bots may still carry one, and the app maps it to a face.
var botIconIDs = strings.Fields(botIconList)

// botFaces are the faces a bot can wear, drawn by src/BotFace.tsx: one per
// kind of job, plus a plain bot.
var botFaces = []string{"bot", "travel", "writer", "data", "news", "research", "fitness", "chef", "money", "inbox", "code", "planner", "music"}

// botColors are the soft, slightly washed-out colours offered for a bot's face; the same list as src/BotFace.tsx.
var botColors = []string{"#8b93e8", "#e0a35e", "#e8876f", "#62b89c", "#6aa9de", "#a592dc", "#e48aa8", "#86b46e", "#58adbb", "#c58ad6", "#c9a77c", "#8e9bb0"}

func validFace(face string) bool {
	return slices.Contains(botFaces, face) || slices.Contains(botIconIDs, face)
}

const newBotName = "New bot"

func setupPrompt() string {
	return strings.Join([]string{
		"You are a brand-new bot in Rex, a personal bot app on the user's Mac. You do not have a job yet. Your first task is to find out what the user wants from you and set yourself up for it.",
		"How to do it:",
		"1. Greet the user in one or two warm sentences. If your context includes facts about the user (their work, projects, interests), use them to make your suggestions specific.",
		"2. Guess from your name what the user most likely wants. Ask what you should focus on with request_user_input: one question with your 3 or 4 best guesses as options, each with a short label and a one-line description. The user can also type their own answer.",
		"3. Ask at most three short follow-up questions, one at a time, with request_user_input and options. Pin down: the goal, the sites, files or tools involved, whether this is on demand or on a schedule, and what a good result looks like. Skip questions the user already answered.",
		"4. Call mcp__setup__configure_myself with a name (keep the one the user gave you unless they ask for another), the face that fits your job and clear instructions written to yourself in the second person (\"You ...\"). Include what you learned: goal, sources, preferences, output format.",
		"5. If the job needs a browser, reading or editing files, or running commands, call mcp__setup__enable_tools. The user is asked to approve; if they decline, carry on without it.",
		"6. If the job should repeat on a schedule, call mcp__setup__create_routine. The user is asked to approve it.",
		"7. Search the skill catalog with mcp__catalog__find_skills_in_catalog for the main kinds of work in your job (for example \"pdf report\", \"cold email\", \"seo\"). If a skill clearly fits, suggest it with mcp__catalog__suggest_skill and one sentence on why; suggest at most two. Skip this when nothing fits.",
		"If the job needs signing in to a site, tell the user to add a login under Edit bot → Logins once you are set up. Never ask for a password in chat.",
		"8. Finish with two sentences introducing yourself under your new name and offer to start right away.",
		"Always ask through request_user_input, never as a plain-text question, so the user can tap an option. Keep every message short. Do not do the actual task until you are set up.",
	}, "\n")
}

// SetupToken is the bearer token for one bot's setup tools.
func (h *Host) SetupToken(botID string) string {
	mac := hmac.New(sha256.New, h.cfg.MCPSecret)
	mac.Write([]byte("bot-setup:" + botID))
	return hex.EncodeToString(mac.Sum(nil))
}

func (h *Host) setupMCPServer(bot Bot) RunMCPServer {
	return RunMCPServer{
		ServerID: "rex-setup", ServerName: "setup", Transport: "streamable_http",
		URL: h.cfg.HostURL + "/mcp/setup/" + bot.ID,
		// Configuring itself only changes the bot's own name and
		// instructions. Tools and routines are writes: they need approval.
		Tools:      []RunMCPTool{{Name: "configure_myself", Access: "read"}, {Name: "enable_tools", Access: "write"}, {Name: "create_routine", Access: "write"}},
		Credential: &RunMCPCredential{Type: "bearer_token", AccessToken: h.SetupToken(bot.ID)},
	}
}

// setupMCP serves the setup tools of the bot named in the URL.
func (h *Host) setupMCP() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		botID := strings.TrimPrefix(r.URL.Path, "/mcp/setup/")
		if botID == "" || !bearerMatches(r, h.SetupToken(botID)) {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
			return
		}
		server := protocol.NewServer(&protocol.Implementation{Name: "rex-setup", Version: "1.0.0"}, nil)
		server.AddTool(&protocol.Tool{
			Name:        "configure_myself",
			Description: "Set your name, face and instructions once you know what the user wants. Pick the face that fits your job: " + strings.Join(botFaces, ", ") + " (bot is a plain robot). The user already chose your colour.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"name", "face", "instructions"}, "properties": map[string]any{
				"name":         map[string]any{"type": "string", "minLength": 2, "maxLength": 40},
				"face":         map[string]any{"type": "string", "enum": botFaces},
				"instructions": map[string]any{"type": "string", "minLength": 20, "maxLength": 6000},
			}},
		}, func(ctx context.Context, request *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			var input struct {
				Name         string `json:"name"`
				Face         string `json:"face"`
				Instructions string `json:"instructions"`
			}
			if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
				return toolError(err), nil
			}
			bot, err := h.ConfigureBot(ctx, botID, input.Name, input.Face, input.Instructions)
			if err != nil {
				return toolError(err), nil
			}
			return toolResult(map[string]any{"status": "configured", "name": bot.Name, "message": "Saved. Your new name and instructions apply from the user's next message."}), nil
		})
		server.AddTool(&protocol.Tool{
			Name:        "enable_tools",
			Description: "Turn on tools your job needs. The user is asked to approve. browser: your own browser with a persistent profile. files_read: read files in your workspace folder. files_write: create and edit files there. shell: run commands and Python, sandboxed to your folder.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
				"browser": map[string]any{"type": "boolean"}, "files_read": map[string]any{"type": "boolean"},
				"files_write": map[string]any{"type": "boolean"}, "shell": map[string]any{"type": "boolean"},
			}},
		}, func(ctx context.Context, request *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			var input struct {
				Browser    bool `json:"browser"`
				FilesRead  bool `json:"files_read"`
				FilesWrite bool `json:"files_write"`
				Shell      bool `json:"shell"`
			}
			if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
				return toolError(err), nil
			}
			enabled, err := h.EnableBotTools(ctx, botID, input.Browser, input.FilesRead, input.FilesWrite, input.Shell)
			if err != nil {
				return toolError(err), nil
			}
			return toolResult(map[string]any{"status": "enabled", "tools": enabled, "message": "Enabled. The tools are available from the user's next message."}), nil
		})
		server.AddTool(&protocol.Tool{
			Name:        "create_routine",
			Description: "Schedule a task you run by yourself: daily at a time, every N minutes (at least 15), or on a calendar with a cron expression such as \"0 8 * * 1-5\" (weekdays at 08:00, local time). The user is asked to approve.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"title", "prompt", "schedule"}, "properties": map[string]any{
				"title":            map[string]any{"type": "string", "minLength": 3, "maxLength": 60},
				"prompt":           map[string]any{"type": "string", "minLength": 10, "maxLength": 2000, "description": "What to do each time, written as an instruction to yourself."},
				"schedule":         map[string]any{"type": "string", "enum": []string{"daily", "interval", "cron"}},
				"cron":             map[string]any{"type": "string", "description": "Five-field cron expression for schedule cron."},
				"daily_time":       map[string]any{"type": "string", "pattern": "^[0-2][0-9]:[0-5][0-9]$", "description": "Local time for daily routines, HH:MM."},
				"interval_minutes": map[string]any{"type": "integer", "minimum": MinIntervalMinutes},
			}},
		}, func(ctx context.Context, request *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			var input struct {
				Title           string `json:"title"`
				Prompt          string `json:"prompt"`
				Schedule        string `json:"schedule"`
				DailyTime       string `json:"daily_time"`
				IntervalMinutes int    `json:"interval_minutes"`
				Cron            string `json:"cron"`
			}
			if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
				return toolError(err), nil
			}
			routine := Routine{BotID: botID, Title: input.Title, Prompt: input.Prompt, ScheduleKind: input.Schedule, DailyTime: input.DailyTime, IntervalMinutes: input.IntervalMinutes, Cron: input.Cron, Enabled: true}
			if err := h.CreateRoutine(ctx, &routine); err != nil {
				return toolError(err), nil
			}
			return toolResult(map[string]any{"status": "scheduled", "routine_id": routine.ID, "next_run_at": routine.NextRunAt}), nil
		})
		protocol.NewStreamableHTTPHandler(func(*http.Request) *protocol.Server { return server }, &protocol.StreamableHTTPOptions{Stateless: true, JSONResponse: true}).ServeHTTP(w, r)
	})
}

// ConfigureBot applies what a new bot decided about itself and ends setup.
func (h *Host) ConfigureBot(ctx context.Context, botID, name, face, instructions string) (*Bot, error) {
	bot, err := h.Store.GetBot(ctx, botID)
	if err != nil {
		return nil, err
	}
	name, instructions = strings.TrimSpace(name), strings.TrimSpace(instructions)
	if name == "" || instructions == "" {
		return nil, errors.New("name and instructions are required")
	}
	if !validFace(face) {
		return nil, fmt.Errorf("unknown face %q", face)
	}
	bot.Name, bot.Emoji, bot.Instructions = name, face, instructions
	if bot.Setup && !bot.UsesWorkspace() {
		// No files yet: name the workspace folder after the new name.
		bot.WorkspaceDir = ""
	}
	bot.Setup = false
	if err := h.SaveBot(ctx, bot); err != nil {
		return nil, err
	}
	h.refreshAfterSetup(ctx, bot.ID)
	return bot, nil
}

// EnableBotTools turns on capabilities the user approved.
func (h *Host) EnableBotTools(ctx context.Context, botID string, browser, filesRead, filesWrite, shell bool) ([]string, error) {
	bot, err := h.Store.GetBot(ctx, botID)
	if err != nil {
		return nil, err
	}
	if (filesWrite || shell) && !h.WriteTools() {
		return nil, ErrWriteToolsUnavailable
	}
	var enabled []string
	if browser {
		bot.Capabilities.Browser = true
		enabled = append(enabled, "browser")
	}
	if filesRead || filesWrite || shell {
		bot.Capabilities.FilesRead = true
		enabled = append(enabled, "files_read")
	}
	if filesWrite {
		bot.Capabilities.FilesWrite = true
		enabled = append(enabled, "files_write")
	}
	if shell {
		bot.Capabilities.Shell = true
		enabled = append(enabled, "shell")
	}
	if len(enabled) == 0 {
		return nil, errors.New("no tools were requested")
	}
	if err := h.SaveBot(ctx, bot); err != nil {
		return nil, err
	}
	h.refreshAfterSetup(ctx, bot.ID)
	return enabled, nil
}

// CreateRoutine validates and saves a new routine.
func (h *Host) CreateRoutine(ctx context.Context, routine *Routine) error {
	if err := routine.Validate(time.Now()); err != nil {
		return err
	}
	if _, err := h.Store.GetBot(ctx, routine.BotID); err != nil {
		return errors.New("bot not found")
	}
	if count, _ := h.Store.CountEnabledRoutines(ctx, ""); count >= MaxEnabledRoutines {
		return fmt.Errorf("at most %d routines can be enabled", MaxEnabledRoutines)
	}
	return h.Store.SaveRoutine(ctx, routine)
}

// refreshAfterSetup makes the bot's next message start a new run, so its
// new instructions and tools apply while the conversation carries over.
func (h *Host) refreshAfterSetup(ctx context.Context, botID string) {
	h.detachMu.Lock()
	h.detachAfter[botID] = true
	h.detachMu.Unlock()
}

// detachIfConfigured runs when a bot's turn settles.
func (h *Host) detachIfConfigured(ctx context.Context, botID, conversationID string) {
	h.detachMu.Lock()
	pending := h.detachAfter[botID]
	delete(h.detachAfter, botID)
	h.detachMu.Unlock()
	if pending {
		if err := h.Store.DetachRun(ctx, conversationID); err != nil {
			slog.Warn("could not refresh the bot after setup", "bot", botID, "error", err)
		}
	}
}

// DraftBot creates a new bot in setup mode from the name the user gave it,
// opens its conversation and lets it greet the user and ask what to do.
func (h *Host) DraftBot(ctx context.Context, name, face, color string) (*Bot, error) {
	return h.DraftBotFor(ctx, name, face, color, "")
}

// DraftBotFor is DraftBot with a purpose, when Rex creates a bot for a job.
func (h *Host) DraftBotFor(ctx context.Context, name, face, color, purpose string) (*Bot, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = newBotName
	}
	if !validFace(face) {
		face = "bot"
	}
	if !slices.Contains(botColors, color) {
		color = botColors[rand.IntN(len(botColors))]
	}
	settings, err := h.Settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	bots, _ := h.Store.ListBots(ctx)
	bot := &Bot{
		Name: name, Emoji: face, Color: color,
		Provider: settings.DefaultProvider, Model: settings.DefaultModel,
		Capabilities: Capabilities{Web: true, Memory: true}, ApprovalMode: ApprovalAsk, MemoryMode: MemoryPrivate,
		Position: len(bots), Setup: true,
	}
	if err := h.SaveBot(ctx, bot); err != nil {
		return nil, err
	}
	conversation, err := h.Store.ThreadForBot(ctx, *bot)
	if err != nil {
		return nil, err
	}
	if h.RuntimeReady() {
		kickoff := fmt.Sprintf("The user just created you and named you %q. Do not answer with text. Call request_user_input now with one question: put a warm one-sentence greeting in header, \"What should I focus on first?\" in question, and 3 or 4 options that are your best guesses from your name, each with a label and a one-line description.", name)
		if purpose = strings.TrimSpace(purpose); purpose != "" {
			kickoff += " Rex, the user's main assistant, created you for this: " + purpose + " Base your options on it."
		}
		if err := h.StartBotTurn(ctx, *bot, conversation, "", "New bot", kickoff); err != nil {
			slog.Warn("new bot could not greet the user", "error", err)
		}
	}
	return bot, nil
}

// StartBotTurn starts a turn the bot begins itself, such as a new bot's
// greeting. userText may be empty; the transcript then shows only the bot.
func (h *Host) StartBotTurn(ctx context.Context, bot Bot, conversation *Conversation, userText, section, instructions string) error {
	turn := &Turn{ConversationID: conversation.ID, BotID: bot.ID, UserText: userText, MemoryStatus: "skipped", Section: section}
	if err := h.Store.CreateTurn(ctx, turn); err != nil {
		return err
	}
	run, err := h.startRun(ctx, bot, conversation, instructions, true, "turn-"+turn.ID)
	if err != nil {
		_ = h.Store.DeleteTurn(ctx, turn.ID)
		return err
	}
	_ = h.Store.SetTurnRun(ctx, turn.ID, run.ID)
	h.Watch(run.ID)
	return nil
}

func (h *Host) draftBot(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name  string `json:"name"`
		Face  string `json:"face"`
		Color string `json:"color"`
	}
	if !decode(w, r, &input) {
		return
	}
	bot, err := h.DraftBot(r.Context(), input.Name, input.Face, input.Color)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, bot)
}
