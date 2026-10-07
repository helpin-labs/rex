package rex

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

// A bot can propose new instructions for itself when the user (or Rex on
// the user's behalf) asks it to change how it works: sources, format,
// tone. Instructions apply to every run, routines included, unlike
// memory, which is recalled only when it seems relevant. A proposal never
// applies by itself: Rex shows it in the bot's conversation and the user
// applies or discards it, whatever the bot's approval mode.

type InstructionProposal struct {
	ID           string    `json:"id"`
	BotID        string    `json:"bot_id"`
	Instructions string    `json:"instructions"`
	Summary      string    `json:"summary"`
	CreatedAt    time.Time `json:"created_at"`
}

// SelfToken is the bearer token for one bot's self tools.
func (h *Host) SelfToken(botID string) string {
	mac := hmac.New(sha256.New, h.cfg.MCPSecret)
	mac.Write([]byte("bot-self:" + botID))
	return hex.EncodeToString(mac.Sum(nil))
}

func (h *Host) selfMCPServer(bot Bot) RunMCPServer {
	return RunMCPServer{
		ServerID: "rex-self", ServerName: "self", Transport: "streamable_http",
		URL: h.cfg.HostURL + "/mcp/self/" + bot.ID,
		// Proposing changes nothing; the user applies it in the app.
		Tools:      []RunMCPTool{{Name: "update_my_instructions", Access: "read"}},
		Credential: &RunMCPCredential{Type: "bearer_token", AccessToken: h.SelfToken(bot.ID)},
	}
}

func (h *Host) selfMCP() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		botID := strings.TrimPrefix(r.URL.Path, "/mcp/self/")
		if botID == "" || !bearerMatches(r, h.SelfToken(botID)) {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
			return
		}
		server := protocol.NewServer(&protocol.Implementation{Name: "rex-self", Version: "1.0.0"}, nil)
		server.AddTool(&protocol.Tool{
			Name: "update_my_instructions",
			Description: "Propose new instructions for yourself when the user asks you to change how you work (sources, format, tone, what to include). " +
				"Send the complete new text of your instructions from the user (the part under \"Your instructions from the user\"), keeping everything that still applies. Do not include Rex's operating rules or skills. The user must apply it in the app; until then nothing has changed.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"instructions", "summary"}, "properties": map[string]any{
				"instructions": map[string]any{"type": "string", "minLength": 20, "maxLength": 8000, "description": "Your complete new instructions, in the second person."},
				"summary":      map[string]any{"type": "string", "minLength": 5, "maxLength": 300, "description": "One sentence on what changes, for the user."},
			}},
		}, func(ctx context.Context, request *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			var input struct {
				Instructions string `json:"instructions"`
				Summary      string `json:"summary"`
			}
			if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
				return toolError(err), nil
			}
			if _, err := h.Store.ProposeInstructions(ctx, botID, input.Instructions, input.Summary); err != nil {
				return toolError(err), nil
			}
			return toolResult(map[string]any{"status": "proposed", "message": "Proposed. The user sees the change and applies or discards it. Tell them what you proposed; do not say it is done."}), nil
		})
		protocol.NewStreamableHTTPHandler(func(*http.Request) *protocol.Server { return server }, &protocol.StreamableHTTPOptions{Stateless: true, JSONResponse: true}).ServeHTTP(w, r)
	})
}

// ProposeInstructions records a bot's proposed instructions, replacing an
// earlier proposal the user has not answered.
func (s *Store) ProposeInstructions(ctx context.Context, botID, instructions, summary string) (*InstructionProposal, error) {
	instructions, summary = userInstructions(instructions), strings.TrimSpace(summary)
	if len(instructions) < 20 {
		return nil, errors.New("send the complete new instructions")
	}
	if _, err := s.GetBot(ctx, botID); err != nil {
		return nil, err
	}
	item := &InstructionProposal{ID: uuid.NewString(), BotID: botID, Instructions: instructions, Summary: summary, CreatedAt: time.Now().UTC()}
	err := withTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM instruction_proposals WHERE bot_id=?`, botID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO instruction_proposals(id,bot_id,instructions,summary,created_at) VALUES(?,?,?,?,?)`, item.ID, item.BotID, item.Instructions, item.Summary, item.CreatedAt)
		return err
	})
	return item, err
}

// userInstructions keeps only the user's part when a bot copies its whole
// system prompt, so Rex's operating rules never end up in the
// instructions (they are added to every run already).
func userInstructions(text string) string {
	text = strings.TrimSpace(text)
	const marker = "Your instructions from the user:"
	if index := strings.Index(text, marker); index >= 0 {
		text = text[index+len(marker):]
	}
	for _, end := range []string{"\n\nOperating rules:", "\nOperating rules:", "\n\nSkill: "} {
		if index := strings.Index(text, end); index >= 0 {
			text = text[:index]
		}
	}
	return strings.TrimSpace(text)
}

func (s *Store) PendingProposals(ctx context.Context) ([]InstructionProposal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,bot_id,instructions,summary,created_at FROM instruction_proposals ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []InstructionProposal{}
	for rows.Next() {
		var item InstructionProposal
		if err := rows.Scan(&item.ID, &item.BotID, &item.Instructions, &item.Summary, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) takeProposal(ctx context.Context, id string) (*InstructionProposal, error) {
	var item InstructionProposal
	err := s.db.QueryRowContext(ctx, `DELETE FROM instruction_proposals WHERE id=? RETURNING id,bot_id,instructions,summary,created_at`, id).
		Scan(&item.ID, &item.BotID, &item.Instructions, &item.Summary, &item.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &item, err
}

// AnswerProposal applies or discards a proposal. Applying changes the
// bot's instructions; its conversation continues in a new run so they take
// effect from the next message.
func (h *Host) AnswerProposal(ctx context.Context, id string, apply bool) error {
	proposal, err := h.Store.takeProposal(ctx, id)
	if err != nil {
		return err
	}
	if !apply {
		return nil
	}
	bot, err := h.Store.GetBot(ctx, proposal.BotID)
	if err != nil {
		return err
	}
	bot.Instructions = proposal.Instructions
	if err := h.SaveBot(ctx, bot); err != nil {
		return err
	}
	if thread, err := h.Store.ThreadForBot(ctx, *bot); err == nil {
		if thread.ActiveRunID != nil {
			if run, err := h.Runtime.GetRun(ctx, *thread.ActiveRunID); err == nil && (run.Status == "running" || run.Status == "queued") {
				h.refreshAfterSetup(ctx, bot.ID)
				return nil
			}
		}
		return h.Store.DetachRun(ctx, thread.ID)
	}
	return nil
}

func (h *Host) proposals(w http.ResponseWriter, r *http.Request) {
	items, err := h.Store.PendingProposals(r.Context())
	writeResult(w, items, err)
}

func (h *Host) answerProposal(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Action string `json:"action"` // apply or discard
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Action != "apply" && input.Action != "discard" {
		writeError(w, http.StatusBadRequest, errors.New("action must be apply or discard"))
		return
	}
	if err := h.AnswerProposal(r.Context(), r.PathValue("id"), input.Action == "apply"); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
