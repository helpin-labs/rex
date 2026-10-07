package rex

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The main bot is Rex itself: listed first, it handles small tasks and
// hands larger ones to the other bots. A handed-over task appears in that
// bot's conversation marked "from Rex" and runs with that bot's own tools
// and approvals; when it finishes, a report comes back to Rex's
// conversation. Rex keeps only high-level memory about the user and never
// reads other bots' memories.

const mainBotName = "Rex"

// MainBot returns the main bot, or nil when there is none.
func (h *Host) MainBot(ctx context.Context) *Bot {
	bots, err := h.Store.ListBots(ctx)
	if err != nil {
		return nil
	}
	for _, bot := range bots {
		if bot.Role == RoleMain {
			return &bot
		}
	}
	return nil
}

// EnsureMainBot makes sure there is a main bot: the bot named Rex from the
// first run, or a new one.
func (h *Host) EnsureMainBot(ctx context.Context) error {
	if h.MainBot(ctx) != nil {
		return nil
	}
	bots, err := h.Store.ListBots(ctx)
	if err != nil {
		return err
	}
	for _, bot := range bots {
		if bot.Name == mainBotName {
			bot.Role = RoleMain
			return h.Store.SaveBot(ctx, &bot)
		}
	}
	settings, err := h.Settings.Get(ctx)
	if err != nil {
		return err
	}
	main := &Bot{
		Name: mainBotName, Emoji: "compass", Color: "#8b93e8", Role: RoleMain,
		Provider: settings.DefaultProvider, Model: settings.DefaultModel,
		Capabilities: Capabilities{Web: true}, ApprovalMode: ApprovalAsk, MemoryMode: MemoryPrivate,
	}
	if err := h.SaveBot(ctx, main); err != nil {
		return err
	}
	_, err = h.Store.ThreadForBot(ctx, *main)
	return err
}

func mainPrompt() string {
	return strings.Join([]string{
		"You are Rex, the user's main assistant. The user's other bots each do one kind of job in their own conversation; your context lists them.",
		"- Handle quick things yourself: answers, short lookups, a bit of writing.",
		"- When a request fits one of the bots, or needs tools you do not have, hand it over with mcp__bots__delegate_task. The bot cannot see this conversation: put everything it needs in the task and context (the actual lists, names, links and numbers), never \"the sources above\" or \"the latest search\". If the user wants a bot to change how it works from now on, say so in the task; the bot proposes new instructions for the user to apply. Then tell the user in one sentence who is on it. The bot reports back here when it is done; when you receive a report, give the user the result in a few lines.",
		"- If no bot fits and the work is recurring or specialised, offer to create one with mcp__bots__create_bot. It will ask the user a few questions in its own conversation.",
		"- Use mcp__bots__list_bots to check what a bot does or whether it is busy.",
		"- Keep only high-level memory about the user: their work, projects, people and preferences. Task details stay with each bot. You cannot see other bots' memories.",
	}, "\n")
}

// botRoster describes the other bots for the main bot's context.
func (h *Host) botRoster(ctx context.Context) ([]map[string]any, error) {
	bots, err := h.Store.ListBots(ctx)
	if err != nil {
		return nil, err
	}
	var roster []map[string]any
	for _, bot := range bots {
		if bot.Role == RoleMain {
			continue
		}
		status := "idle"
		if bot.Setup {
			status = "setting up"
		} else if thread, err := h.Store.ThreadForBot(ctx, bot); err == nil {
			if turn, err := h.Store.LatestTurnForConversation(ctx, thread.ID); err == nil && turn.Status == "working" {
				status = "working"
			}
		}
		var tools []string
		for name, on := range map[string]bool{"browser": bot.Capabilities.Browser, "files": bot.Capabilities.FilesRead, "commands": bot.Capabilities.Shell, "web": bot.Capabilities.Web} {
			if on {
				tools = append(tools, name)
			}
		}
		roster = append(roster, map[string]any{"id": bot.ID, "name": bot.Name, "purpose": purpose(bot.Instructions), "tools": strings.Join(sorted(tools), ", "), "status": status})
	}
	return roster, nil
}

func purpose(instructions string) string {
	text := strings.Join(strings.Fields(instructions), " ")
	if runes := []rune(text); len(runes) > 240 {
		return string(runes[:239]) + "…"
	}
	return text
}

func sorted(values []string) []string {
	sort.Strings(values)
	return values
}

// rosterContext is the bot list shown to the main bot each turn.
func (h *Host) rosterContext(ctx context.Context) string {
	roster, err := h.botRoster(ctx)
	if err != nil || len(roster) == 0 {
		return "The user has no other bots yet."
	}
	lines := []string{"The user's bots:"}
	for _, bot := range roster {
		line := fmt.Sprintf("- %s (id %s, %s)", bot["name"], bot["id"], bot["status"])
		if tools := bot["tools"].(string); tools != "" {
			line += "; tools: " + tools
		}
		if p := bot["purpose"].(string); p != "" {
			line += ": " + p
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// MainToken is the bearer token for the main bot's tools.
func (h *Host) MainToken(botID string) string {
	mac := hmac.New(sha256.New, h.cfg.MCPSecret)
	mac.Write([]byte("main-bot:" + botID))
	return hex.EncodeToString(mac.Sum(nil))
}

func (h *Host) botsMCPServer(main Bot) RunMCPServer {
	return RunMCPServer{
		ServerID: "rex-bots", ServerName: "bots", Transport: "streamable_http",
		URL: h.cfg.HostURL + "/mcp/bots/" + main.ID,
		// Handing over a task is like the user messaging that bot: it runs
		// with the bot's own tools and approvals. Creating a bot asks first.
		Tools:      []RunMCPTool{{Name: "list_bots", Access: "read"}, {Name: "delegate_task", Access: "read"}, {Name: "create_bot", Access: "write"}},
		Credential: &RunMCPCredential{Type: "bearer_token", AccessToken: h.MainToken(main.ID)},
	}
}

// botsMCP serves the main bot's tools.
func (h *Host) botsMCP() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mainID := strings.TrimPrefix(r.URL.Path, "/mcp/bots/")
		main, err := h.Store.GetBot(r.Context(), mainID)
		if err != nil || main.Role != RoleMain || !bearerMatches(r, h.MainToken(mainID)) {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
			return
		}
		server := protocol.NewServer(&protocol.Implementation{Name: "rex-bots", Version: "1.0.0"}, nil)
		server.AddTool(&protocol.Tool{
			Name:        "list_bots",
			Description: "List the user's bots with what each one does, its tools and whether it is busy.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}},
		}, func(ctx context.Context, _ *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			roster, err := h.botRoster(ctx)
			if err != nil {
				return toolError(err), nil
			}
			return toolResult(map[string]any{"bots": roster}), nil
		})
		server.AddTool(&protocol.Tool{
			Name:        "delegate_task",
			Description: "Hand a task to one of the user's bots. It appears in that bot's conversation, the bot works with its own tools, and its result is reported back to you here when it finishes.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"bot_id", "task"}, "properties": map[string]any{
				"bot_id":  map[string]any{"type": "string"},
				"task":    map[string]any{"type": "string", "minLength": 5, "maxLength": 4000, "description": "The complete task, written so the bot can act on it alone."},
				"context": map[string]any{"type": "string", "maxLength": 12000, "description": "Material from this conversation the bot needs: the actual items, links, numbers or text. The bot cannot see this conversation."},
			}},
		}, func(ctx context.Context, request *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			var input struct {
				BotID   string `json:"bot_id"`
				Task    string `json:"task"`
				Context string `json:"context"`
			}
			if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
				return toolError(err), nil
			}
			name, err := h.Delegate(ctx, *main, input.BotID, input.Task, input.Context)
			if err != nil {
				return toolError(err), nil
			}
			return toolResult(map[string]any{"status": "handed_over", "bot": name, "message": name + " is working on it and will report back here."}), nil
		})
		server.AddTool(&protocol.Tool{
			Name:        "create_bot",
			Description: "Create a new bot for a job none of the bots fits. The user is asked to approve; the new bot then asks them a few questions in its own conversation.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"name", "purpose"}, "properties": map[string]any{
				"name":    map[string]any{"type": "string", "minLength": 2, "maxLength": 40},
				"purpose": map[string]any{"type": "string", "minLength": 5, "maxLength": 1000},
			}},
		}, func(ctx context.Context, request *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			var input struct {
				Name    string `json:"name"`
				Purpose string `json:"purpose"`
			}
			if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
				return toolError(err), nil
			}
			bot, err := h.DraftBotFor(ctx, input.Name, "bot", "", input.Purpose)
			if err != nil {
				return toolError(err), nil
			}
			return toolResult(map[string]any{"status": "created", "bot_id": bot.ID, "message": bot.Name + " was created and is asking the user a few questions in its own conversation."}), nil
		})
		protocol.NewStreamableHTTPHandler(func(*http.Request) *protocol.Server { return server }, &protocol.StreamableHTTPOptions{Stateless: true, JSONResponse: true}).ServeHTTP(w, r)
	})
}

// Delegate hands a task from the main bot to another bot's conversation.
func (h *Host) Delegate(ctx context.Context, main Bot, botID, task, material string) (string, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return "", errors.New("the task is empty")
	}
	bot, err := h.Store.GetBot(ctx, botID)
	if err != nil {
		return "", fmt.Errorf("no bot with id %q; use list_bots", botID)
	}
	if bot.Role == RoleMain {
		return "", errors.New("do this task yourself")
	}
	if bot.Setup {
		return "", fmt.Errorf("%s is still being set up by the user", bot.Name)
	}
	mainThread, err := h.Store.ThreadForBot(ctx, main)
	if err != nil {
		return "", err
	}
	thread, err := h.Store.ThreadForBot(ctx, *bot)
	if err != nil {
		return "", err
	}
	message := "Task from Rex, the user's main assistant, on the user's behalf:\n\n" + task
	shown := task
	if material = strings.TrimSpace(material); material != "" {
		message += "\n\nMaterial from Rex's conversation with the user:\n" + material
		shown += "\n\n" + material
	}
	if _, _, err := h.deliver(ctx, thread.ID, delivery{UserText: shown, ModelText: message, Origin: "rex", DelegatedBy: mainThread.ID}); err != nil {
		if errors.Is(err, ErrBusy) {
			return "", fmt.Errorf("%s is busy or waiting for the user; try again later or tell the user", bot.Name)
		}
		return "", err
	}
	return bot.Name, nil
}

// reportToMain sends a handed-over task's outcome back to the main bot's
// conversation. If Rex is busy, it retries for a while.
func (h *Host) reportToMain(turn Turn, status, answer string) {
	go func() {
		ctx := context.Background()
		bot, err := h.Store.GetBot(ctx, turn.BotID)
		if err != nil {
			return
		}
		outcome := map[string]string{"settled": "finished", "failed": "could not finish", "cancelled": "was stopped before finishing"}[status]
		if outcome == "" {
			outcome = "stopped"
		}
		result := strings.TrimSpace(answer)
		if runes := []rune(result); len(runes) > 4000 {
			result = string(runes[:3999]) + "…"
		}
		report := fmt.Sprintf("Report: %s %s the task you handed over.\n\nTask:\n%s\n\nResult:\n%s\n\nTell the user the outcome in a few lines.", bot.Name, outcome, turn.UserText, result)
		for attempt := 0; attempt < 180; attempt++ {
			_, _, err := h.deliver(ctx, turn.DelegatedBy, delivery{ModelText: report, Origin: "report:" + bot.Name})
			if err == nil {
				return
			}
			if !errors.Is(err, ErrBusy) && h.RuntimeReady() {
				slog.Warn("could not report back to Rex", "bot", bot.ID, "error", err)
				return
			}
			time.Sleep(10 * time.Second)
		}
	}()
}
