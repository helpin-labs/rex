package rex

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed app_catalog.json
var appCatalogJSON []byte

type AppCapability struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Auth        string `json:"auth"`
	Access      string `json:"access,omitempty"`
	TokenHint   string `json:"tokenHint,omitempty"`
}

func appCapabilities() []AppCapability {
	var apps []AppCapability
	_ = json.Unmarshal(appCatalogJSON, &apps)
	return apps
}
func findApp(id string) *AppCapability {
	for _, app := range appCapabilities() {
		if app.ID == id {
			return &app
		}
	}
	return nil
}

type ProposalEvidence struct {
	LessonID               string   `json:"lesson_id,omitempty"`
	GapType                string   `json:"gap_type"`
	Sources                []string `json:"sources"`
	Gap                    string   `json:"gap"`
	MemoryRemedy           string   `json:"memory_remedy"`
	MemoryInsufficient     string   `json:"memory_insufficient"`
	CapabilityRemedy       string   `json:"capability_remedy"`
	CapabilityInsufficient string   `json:"capability_insufficient"`
	Documentation          []string `json:"documentation"`
	Access                 string   `json:"access"`
	SetupCost              string   `json:"setup_cost"`
	Expected               string   `json:"expected"`
	NewEvidence            string   `json:"new_evidence,omitempty"`
}
type memoryReview struct {
	MemoryAt     *time.Time        `json:"memory_at,omitempty"`
	Available    bool              `json:"available"`
	Pending      int               `json:"pending"`
	Failed       int               `json:"failed"`
	CapabilityAt *time.Time        `json:"capability_at,omitempty"`
	Candidates   map[string]string `json:"candidates,omitempty"`
	Remedy       string            `json:"remedy,omitempty"`
}

func (h *Host) saveReview(ctx context.Context, id string, review memoryReview) error {
	body, _ := json.Marshal(review)
	result, err := h.Store.db.ExecContext(ctx, `UPDATE retrospectives SET review=? WHERE id=? AND status='running'`, string(body), id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errors.New("review is no longer running")
	}
	return nil
}
func (h *Host) reviewMemory(ctx context.Context, retroID string, bot Bot, action, source string) (map[string]any, error) {
	retro, err := h.Store.GetRetrospective(ctx, retroID)
	if err != nil {
		return nil, err
	}
	var review memoryReview
	_ = json.Unmarshal(retro.Review, &review)
	bank := firstNonEmpty(bot.MemoryBank(), "bot:"+bot.ID)
	if action == "retry" {
		if !strings.HasPrefix(source, "episode/") {
			return nil, errors.New("retry needs an episode source ID")
		}
		if err := h.Store.retryEpisode(ctx, bank, strings.TrimPrefix(source, "episode/")); err != nil {
			return nil, err
		}
		h.Memory.Notify()
		review.Remedy = "recorded learning queued for retry"
	} else if action == "consolidate" {
		state, err := h.Store.maintenance(ctx, bank)
		if err != nil {
			return nil, err
		}
		if state.LastSuccess == nil || time.Since(*state.LastSuccess) >= 30*time.Minute {
			if _, err := h.Memory.consolidateBank(ctx, bank); err != nil {
				return nil, err
			}
			review.Remedy = "source-backed memory consolidated"
		} else {
			review.Remedy = "memory consolidation is current"
		}
	} else if action != "" && action != "review" {
		return nil, errors.New("action must be review, retry or consolidate")
	}
	counts, err := h.Store.episodeProgress(ctx, bank)
	if err != nil {
		return nil, err
	}
	lessons, err := h.Memory.Lessons(ctx, bank)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	review.MemoryAt = &now
	review.Available = h.Memory.Available() && bot.MemoryBank() != ""
	review.Pending = counts["capture"] + counts["queued"] + counts["extracting"]
	review.Failed = counts["failed"]
	if err := h.saveReview(ctx, retroID, review); err != nil {
		return nil, err
	}
	state, _ := h.Store.maintenance(ctx, bank)
	turns, err := h.Store.settledTurnsSince(ctx, bot.ID, time.Time{}, 8)
	if err != nil {
		return nil, err
	}
	evidence := []map[string]any{}
	for _, turn := range turns {
		evidence = append(evidence, map[string]any{"source_id": episodeDocument(turn.ID), "status": turn.Status, "request": excerpt(redactSecrets(turn.UserText), 1000), "recalled": turn.Recalled})
	}
	return map[string]any{"counts": counts, "lessons": lessons, "maintenance": state, "model": h.Memory.learningModel(ctx, bank), "memory_available": review.Available, "remedy": review.Remedy, "recent_evidence": evidence, "instruction_gate": "Unavailable, failed or pending learning is a storage/retrieval issue. Repair it before suggesting instructions. History tools expose older evidence."}, nil
}
func (h *Host) capabilitySearch(ctx context.Context, retroID string, bot Bot, query string) (map[string]any, error) {
	if len(query) > 300 {
		return nil, errors.New("use a short generic capability query, without private task details")
	}
	retro, err := h.Store.GetRetrospective(ctx, retroID)
	if err != nil {
		return nil, err
	}
	var review memoryReview
	_ = json.Unmarshal(retro.Review, &review)
	if review.MemoryAt == nil {
		return nil, errors.New("review_memory first")
	}
	if review.Candidates == nil {
		review.Candidates = map[string]string{}
	}
	terms := strings.Fields(strings.ToLower(query))
	matches := func(text string) bool {
		for _, term := range terms {
			if !strings.Contains(strings.ToLower(text), term) {
				return false
			}
		}
		return true
	}
	apps := []AppCapability{}
	for _, app := range appCapabilities() {
		if matches(app.Name + " " + app.Description) {
			apps = append(apps, app)
			review.Candidates["connect_mcp:"+app.ID] = app.URL
		}
		if len(apps) >= 10 {
			break
		}
	}
	skills, err := h.SearchCatalog(ctx, query, 10)
	if err != nil {
		return nil, err
	}
	for _, skill := range skills {
		review.Candidates["install_skill:"+skill.ID] = skill.URL
	}
	connections, err := h.ListMCPConnections(ctx)
	if err != nil {
		return nil, err
	}
	existing := []map[string]any{}
	for _, connection := range connections {
		enabled := false
		for _, id := range bot.MCPConnections {
			if id == connection.ID {
				enabled = true
			}
		}
		existing = append(existing, map[string]any{"id": connection.ID, "name": connection.Name, "enabled_for_bot": enabled, "status": connection.Status, "tools": connection.Tools})
	}
	now := time.Now().UTC()
	review.CapabilityAt = &now
	if err := h.saveReview(ctx, retroID, review); err != nil {
		return nil, err
	}
	installed := []map[string]any{}
	for _, skill := range h.Skills.Resolve(bot.Skills) {
		installed = append(installed, map[string]any{"id": skill.ID, "title": skill.Title, "description": excerpt(skill.Description, 600)})
	}
	return map[string]any{"installed_skills": installed, "connections": existing, "apps": apps, "skills": skills, "checked_at": now, "cost": "Unknown unless separately checked against official documentation. Catalog entries describe capability and authentication; no vendor request was made."}, nil
}
func (h *Host) addRetroLearningTools(server *protocol.Server, id string, bot Bot) {
	server.AddTool(&protocol.Tool{Name: "review_memory", Description: "Review operational episodes, source-backed lessons, consolidation health and recalled evidence before proposing a change. Optionally retry learning for an episode or consolidate existing evidence; never re-run a task action.", InputSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"action": map[string]any{"type": "string", "enum": []string{"review", "retry", "consolidate"}}, "source_id": map[string]any{"type": "string"}}}}, func(ctx context.Context, r *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
		var input struct {
			Action string `json:"action"`
			Source string `json:"source_id"`
		}
		if err := json.Unmarshal(r.Params.Arguments, &input); err != nil {
			return toolError(err), nil
		}
		result, err := h.reviewMemory(ctx, id, bot, input.Action, input.Source)
		if err != nil {
			return toolError(err), nil
		}
		return toolResult(result), nil
	})
	server.AddTool(&protocol.Tool{Name: "capability_search", Description: "Read installed skills, connected MCP capabilities and the Rex skill/Apps catalogs. Use a generic query. Research exact candidates before suggesting a skill or MCP; this never installs, connects or grants access.", InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"query"}, "properties": map[string]any{"query": map[string]any{"type": "string", "maxLength": 300}}}}, func(ctx context.Context, r *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
		var input struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(r.Params.Arguments, &input); err != nil {
			return toolError(err), nil
		}
		result, err := h.capabilitySearch(ctx, id, bot, input.Query)
		if err != nil {
			return toolError(err), nil
		}
		return toolResult(result), nil
	})
}
func (h *Host) validateRetroProposal(ctx context.Context, retro Retrospective, kind, candidate string, proof ProposalEvidence) error {
	var review memoryReview
	_ = json.Unmarshal(retro.Review, &review)
	if review.MemoryAt == nil || review.CapabilityAt == nil {
		return errors.New("call review_memory and capability_search before proposing a change")
	}
	if len(proof.Sources) == 0 || len(proof.Sources) > 10 || strings.TrimSpace(proof.Gap) == "" || strings.TrimSpace(proof.MemoryRemedy) == "" || strings.TrimSpace(proof.MemoryInsufficient) == "" || strings.TrimSpace(proof.CapabilityRemedy) == "" || strings.TrimSpace(proof.CapabilityInsufficient) == "" || strings.TrimSpace(proof.Expected) == "" {
		return errors.New("evidence requires sources, gap, memory_remedy, memory_insufficient, capability_remedy, capability_insufficient and expected behaviour")
	}
	raw, _ := json.Marshal(proof)
	if len(raw) > 12000 {
		return errors.New("proposal evidence is too long")
	}
	bot, err := h.Store.GetBot(ctx, retro.BotID)
	if err != nil {
		return err
	}
	bank := firstNonEmpty(bot.MemoryBank(), "bot:"+bot.ID)
	for _, source := range proof.Sources {
		entry, err := h.Store.ReadHistory(ctx, bank, HistoryRead{SourceID: source, Limit: 100})
		if err != nil {
			return fmt.Errorf("invalid evidence source %q", source)
		}
		episode, err := h.Store.Episode(ctx, entry.TurnID)
		if err != nil || episode.BotID != bot.ID || episode.Forgotten {
			return errors.New("proposal evidence must belong to this bot")
		}
	}
	if kind == "update_instructions" {
		contract, ignored := false, map[string]bool{}
		supported := false
		lessons, _ := h.Memory.Lessons(ctx, bank)
		for _, lesson := range lessons {
			if lesson.ID == proof.LessonID && lesson.State == "supported" {
				supported = true
			}
		}

		for _, source := range proof.Sources {
			entry, _ := h.Store.ReadHistory(ctx, bank, HistoryRead{SourceID: source, Limit: 1})
			turn, err := h.Store.GetTurn(ctx, entry.TurnID)
			if err != nil {
				continue
			}
			if turn.Origin == "" && !isRoutineTurn(*turn) && preferencePattern.MatchString(turn.UserText) {
				contract = true
			}
			if proof.LessonID != "" && (correctionPattern.MatchString(turn.UserText) || turn.Status == "failed") {
				for _, recalled := range turn.Recalled {
					if recalled.SourceID == "lesson/"+proof.LessonID {
						ignored[turn.ID] = true
					}
				}
			}
		}
		if (proof.GapType != "instruction_contract" || !contract) && (proof.GapType != "retrieved_lesson_ignored" || !supported || len(ignored) < 2) {
			return errors.New("instructions require an explicit lasting work contract or repeated failures after relevant lessons were recalled; ordinary procedures belong in memory")
		}

		counts, err := h.Store.episodeProgress(ctx, bank)
		if err != nil {
			return err
		}
		if !review.Available || counts["capture"]+counts["queued"]+counts["extracting"]+counts["failed"] > 0 {
			return errors.New("repair pending or unavailable learning before proposing instructions")
		}
	} else if kind == "install_skill" || kind == "connect_mcp" {
		if proof.GapType != "missing_capability" {
			return errors.New("capability proposals require a missing_capability gap")
		}
		if kind == "connect_mcp" {
			if app := findApp(candidate); app != nil {
				connections, _ := h.ListMCPConnections(ctx)
				for _, connection := range connections {
					if strings.TrimRight(connection.URL, "/") == strings.TrimRight(app.URL, "/") {
						for _, id := range bot.MCPConnections {
							if id == connection.ID {
								return errors.New("this capability is already enabled; repair its existing connection or sign-in in Connect apps")
							}
						}
					}
				}
			}
		}

		document := review.Candidates[kind+":"+candidate]
		if document == "" {
			return errors.New("candidate was not researched with capability_search")
		}
		found := false
		for _, link := range proof.Documentation {
			if link == document {
				found = true
			}
		}
		if !found || proof.Access == "" || proof.SetupCost == "" {
			return errors.New("cite the researched catalog URL, required access and setup/cost (say unknown if unchecked)")
		}
	}
	// A dismissal can be revisited only with sources created after the decision.
	var answered time.Time
	err = h.Store.db.QueryRowContext(ctx, `SELECT answered_at FROM proposal_history WHERE bot_id=? AND (catalog_id=? AND catalog_id<>'' OR instructions=? AND instructions<>'') ORDER BY answered_at DESC LIMIT 1`, bot.ID, candidate, candidate).Scan(&answered)
	if err == nil {
		newSource := false
		for _, source := range proof.Sources {
			entry, _ := h.Store.ReadHistory(ctx, bank, HistoryRead{SourceID: source, Limit: 1})
			if entry.CreatedAt.After(answered) {
				newSource = true
			}
		}
		if !newSource || proof.NewEvidence == "" {
			return errors.New("an earlier decision already covers this suggestion; cite new evidence and explain what changed")
		}
	}
	return nil
}
func (h *Host) proposeMCP(ctx context.Context, botID, appID, summary, reason string, proof ProposalEvidence) (*InstructionProposal, error) {
	app := findApp(appID)
	if app == nil {
		return nil, errors.New("unknown app catalog identity")
	}
	if h.Store.hasPendingProposal(ctx, botID) {
		return nil, errors.New("answer the waiting suggestion first")
	}
	body, _ := json.Marshal(proof)
	proposal := &InstructionProposal{ID: uuid.NewString(), BotID: botID, Kind: ProposalConnectMCP, CatalogID: appID, Summary: firstNonEmpty(summary, "Review "+app.Name+" for this bot"), Reason: reason, Source: ProposalFromRetrospective, CreatedAt: time.Now().UTC(), Evidence: body, App: app}
	_, err := h.Store.db.ExecContext(ctx, `INSERT INTO instruction_proposals(id,bot_id,instructions,summary,source,reason,created_at,kind,catalog_id,evidence) VALUES(?,?,'',?,?,?,?,?,?,?)`, proposal.ID, botID, proposal.Summary, proposal.Source, proposal.Reason, proposal.CreatedAt, proposal.Kind, appID, string(body))
	return proposal, err
}

type jsonScanner struct{ value *json.RawMessage }

func (s jsonScanner) Scan(value any) error {
	switch v := value.(type) {
	case string:
		*s.value = json.RawMessage(v)
	case []byte:
		*s.value = append(json.RawMessage{}, v...)
	default:
		return errors.New("invalid JSON column")
	}
	return nil
}
func firstJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage("{}")
	}
	return value
}

func proposalEvidenceSchema() map[string]any {
	properties := map[string]any{}
	properties["gap_type"] = map[string]any{"type": "string", "enum": []string{"instruction_contract", "retrieved_lesson_ignored", "missing_capability"}}
	for _, name := range []string{"gap", "memory_remedy", "memory_insufficient", "capability_remedy", "capability_insufficient", "access", "setup_cost", "expected", "new_evidence", "lesson_id"} {
		properties[name] = map[string]any{"type": "string", "maxLength": 1200}
	}
	properties["sources"] = map[string]any{"type": "array", "maxItems": 10, "items": map[string]any{"type": "string"}, "description": "Exact history source IDs or episode/<turn_id> for this bot."}
	properties["documentation"] = map[string]any{"type": "array", "maxItems": 10, "items": map[string]any{"type": "string"}, "description": "Include the exact catalog documentation URL returned by capability_search."}
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"sources", "gap", "gap_type", "memory_remedy", "memory_insufficient", "capability_remedy", "capability_insufficient", "expected"}, "properties": properties}
}

func (h *Host) yieldLocalReviews(ctx context.Context) {
	reviews, err := h.Store.retrospectives(ctx, `WHERE status='running'`)
	if err != nil {
		return
	}
	for _, review := range reviews {
		bot, err := h.Store.GetBot(ctx, review.BotID)
		if err != nil || bot.Provider != ProviderLocal || review.RunID == "" {
			continue
		}
		_, _ = h.Store.db.ExecContext(ctx, `UPDATE retrospectives SET status='queued',run_id='',attempts=max(0,attempts-1),retry_at=?,outcome='waiting for local chat' WHERE id=? AND status='running'`, time.Now().UTC().Add(time.Minute), review.ID)
		_, _ = h.Runtime.CancelRun(ctx, review.RunID)
	}
}

func (s *Store) recordReviewOutcome(ctx context.Context, id, outcome string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE retrospectives SET outcome=? WHERE id=? AND status='running' AND outcome=''`, outcome, id)
	return err
}
