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
	"log/slog"
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
	ID           string `json:"id"`
	BotID        string `json:"bot_id"`
	Instructions string `json:"instructions"`
	Summary      string `json:"summary"`
	// Source is "bot" when the user asked the bot to change, or
	// "retrospective" when the bot suggested it after looking back.
	Source    string    `json:"source"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// Kind is "instructions", or "install_skill" for a catalog skill
	// (CatalogID) to install and turn on for the bot.
	Kind      string        `json:"kind"`
	CatalogID string        `json:"catalog_id,omitempty"`
	Skill     *CatalogSkill `json:"skill,omitempty"`
}

const (
	ProposalInstructions = "instructions"
	ProposalInstallSkill = "install_skill"
	maxSkillProposals    = 3
)

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
			if _, err := h.Store.ProposeInstructions(ctx, botID, input.Instructions, input.Summary, ProposalFromBot, ""); err != nil {
				return toolError(err), nil
			}
			return toolResult(map[string]any{"status": "proposed", "message": "Proposed. The user sees the change and applies or discards it. Tell them what you proposed; do not say it is done."}), nil
		})
		protocol.NewStreamableHTTPHandler(func(*http.Request) *protocol.Server { return server }, &protocol.StreamableHTTPOptions{Stateless: true, JSONResponse: true}).ServeHTTP(w, r)
	})
}

// ProposeInstructions records a bot's proposed instructions, replacing an
// earlier proposal the user has not answered.
func (s *Store) ProposeInstructions(ctx context.Context, botID, instructions, summary, source, reason string) (*InstructionProposal, error) {
	instructions, summary, reason = userInstructions(instructions), strings.TrimSpace(summary), strings.TrimSpace(reason)
	if len(instructions) < 20 {
		return nil, errors.New("send the complete new instructions")
	}
	if _, err := s.GetBot(ctx, botID); err != nil {
		return nil, err
	}
	item := &InstructionProposal{ID: uuid.NewString(), BotID: botID, Instructions: instructions, Summary: summary, Source: source, Reason: reason, Kind: ProposalInstructions, CreatedAt: time.Now().UTC()}
	err := withTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM instruction_proposals WHERE bot_id=? AND kind='instructions'`, botID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO instruction_proposals(id,bot_id,instructions,summary,source,reason,created_at) VALUES(?,?,?,?,?,?,?)`, item.ID, item.BotID, item.Instructions, item.Summary, item.Source, item.Reason, item.CreatedAt)
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
	rows, err := s.db.QueryContext(ctx, `SELECT id,bot_id,instructions,summary,source,reason,created_at,kind,catalog_id FROM instruction_proposals ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []InstructionProposal{}
	for rows.Next() {
		var item InstructionProposal
		if err := rows.Scan(&item.ID, &item.BotID, &item.Instructions, &item.Summary, &item.Source, &item.Reason, &item.CreatedAt, &item.Kind, &item.CatalogID); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) takeProposal(ctx context.Context, id string) (*InstructionProposal, error) {
	var item InstructionProposal
	err := s.db.QueryRowContext(ctx, `DELETE FROM instruction_proposals WHERE id=? RETURNING id,bot_id,instructions,summary,source,reason,created_at,kind,catalog_id`, id).
		Scan(&item.ID, &item.BotID, &item.Instructions, &item.Summary, &item.Source, &item.Reason, &item.CreatedAt, &item.Kind, &item.CatalogID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &item, err
}

// AnswerProposal applies or discards a proposal. Applying changes the
// bot's instructions; its conversation continues in a new run so they take
// effect from the next message.
func (h *Host) AnswerProposal(ctx context.Context, id string, apply bool) error {
	if apply {
		// A skill is installed before the proposal is closed, so a failed
		// download can be tried again.
		if pending, err := h.Store.proposal(ctx, id); err == nil && pending.Kind == ProposalInstallSkill {
			if _, err := h.InstallCatalogSkill(ctx, pending.CatalogID, pending.BotID); err != nil {
				return err
			}
		}
	}
	proposal, err := h.Store.takeProposal(ctx, id)
	if err != nil {
		return err
	}
	outcome := "dismissed"
	if apply {
		outcome = "applied"
	}
	if err := h.Store.RecordProposalOutcome(ctx, *proposal, outcome); err != nil {
		slog.Warn("record proposal outcome", "error", err)
	}
	if !apply {
		return nil
	}
	bot, err := h.Store.GetBot(ctx, proposal.BotID)
	if err != nil {
		return err
	}
	if proposal.Kind != ProposalInstallSkill {
		bot.Instructions = proposal.Instructions
		if err := h.SaveBot(ctx, bot); err != nil {
			return err
		}
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
	for index := range items {
		if items[index].CatalogID != "" {
			items[index].Skill, _ = h.Store.CatalogSkill(r.Context(), items[index].CatalogID)
		}
	}
	writeResult(w, items, err)
}

func (s *Store) proposal(ctx context.Context, id string) (*InstructionProposal, error) {
	var item InstructionProposal
	err := s.db.QueryRowContext(ctx, `SELECT id,bot_id,instructions,summary,source,reason,created_at,kind,catalog_id FROM instruction_proposals WHERE id=?`, id).
		Scan(&item.ID, &item.BotID, &item.Instructions, &item.Summary, &item.Source, &item.Reason, &item.CreatedAt, &item.Kind, &item.CatalogID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &item, err
}

// ProposeSkill records a suggestion to install a catalog skill for a bot.
// A bot has at most a few skill suggestions waiting; the same skill is
// suggested once.
func (h *Host) ProposeSkill(ctx context.Context, botID, catalogID, reason, source string) (*InstructionProposal, error) {
	bot, err := h.Store.GetBot(ctx, botID)
	if err != nil {
		return nil, err
	}
	item, err := h.Store.CatalogSkill(ctx, catalogID)
	if err != nil {
		return nil, fmt.Errorf("no skill %q in the catalog; search with find_skills_in_catalog first", catalogID)
	}
	if local := h.installedOrigins()[catalogID]; local != "" && containsString(bot.Skills, local) {
		return nil, fmt.Errorf("%s already has the skill %q", bot.Name, item.Name)
	}
	var waiting int
	_ = h.Store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM instruction_proposals WHERE bot_id=? AND kind='install_skill'`, botID).Scan(&waiting)
	var same int
	_ = h.Store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM instruction_proposals WHERE bot_id=? AND catalog_id=?`, botID, catalogID).Scan(&same)
	if same > 0 {
		return nil, fmt.Errorf("the skill %q is already suggested and waits for the user", item.Name)
	}
	if waiting >= maxSkillProposals {
		return nil, errors.New("several skill suggestions already wait for the user; let them answer those first")
	}
	proposal := &InstructionProposal{ID: uuid.NewString(), BotID: botID, Summary: fmt.Sprintf("Install the skill “%s” from %s", item.Name, item.Repo),
		Source: source, Reason: strings.TrimSpace(reason), Kind: ProposalInstallSkill, CatalogID: catalogID, CreatedAt: time.Now().UTC()}
	_, err = h.Store.db.ExecContext(ctx, `INSERT INTO instruction_proposals(id,bot_id,instructions,summary,source,reason,created_at,kind,catalog_id) VALUES(?,?,'',?,?,?,?,?,?)`,
		proposal.ID, botID, proposal.Summary, proposal.Source, proposal.Reason, proposal.CreatedAt, proposal.Kind, proposal.CatalogID)
	return proposal, err
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
