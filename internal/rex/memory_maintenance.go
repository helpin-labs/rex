package rex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/helpin-ai/agent-runtime/memory"
)

type MemoryMaintenance struct {
	LastSuccess *time.Time                 `json:"last_success,omitempty"`
	RetryAt     *time.Time                 `json:"retry_at,omitempty"`
	Attempts    int                        `json:"attempts"`
	Error       string                     `json:"error,omitempty"`
	Result      memory.ConsolidationResult `json:"result"`
}

func (s *Store) maintenance(ctx context.Context, bank string) (MemoryMaintenance, error) {
	var out MemoryMaintenance
	var last, retry sql.NullTime
	var result string
	err := s.db.QueryRowContext(ctx, `SELECT last_success,retry_at,attempts,error,result FROM memory_maintenance WHERE bank=?`, bank).Scan(&last, &retry, &out.Attempts, &out.Error, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if last.Valid {
		out.LastSuccess = &last.Time
	}
	if retry.Valid {
		out.RetryAt = &retry.Time
	}
	_ = json.Unmarshal([]byte(result), &out.Result)
	return out, err
}

func (m *MemoryService) operationalFactValid(ctx context.Context, bank string, fact memory.Fact) bool {
	if m.store.memoryForgotten(ctx, bank, fact.DocumentID) {
		return false
	}
	if strings.HasPrefix(fact.DocumentID, "turn/") {
		turn, err := m.store.GetTurn(ctx, strings.TrimPrefix(fact.DocumentID, "turn/"))
		return err == nil && turn.MemoryStatus != "forgotten" && turn.MemoryBank == bank
	}
	if !strings.HasPrefix(fact.DocumentID, "episode/") {
		return true
	}
	e, err := m.store.Episode(ctx, strings.TrimPrefix(fact.DocumentID, "episode/"))
	if err != nil || e.Forgotten || e.Bank != bank || fact.Metadata["revision"] != e.Revision {
		return false
	}
	var revisions map[string]string
	if json.Unmarshal([]byte(fact.Metadata["source_revisions"]), &revisions) != nil {
		return false
	}
	for task, revision := range revisions {
		source, err := m.store.Episode(ctx, task)
		if err != nil || source.Bank != bank || source.Forgotten || source.Revision != revision {
			return false
		}
	}
	var ids []string
	if json.Unmarshal([]byte(fact.Metadata["source_ids"]), &ids) != nil || len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		var count int
		if m.store.db.QueryRowContext(ctx, `SELECT count(*) FROM task_history h JOIN task_episodes e ON e.turn_id=h.turn_id WHERE h.id=? AND e.bank=? AND e.forgotten=0`, id, bank).Scan(&count) != nil || count != 1 {
			return false
		}
	}
	return true
}

// Cross-episode citations are checked again after forgetting or editing a source.
func (m *MemoryService) pruneInvalidOperationalFacts(ctx context.Context, bank string) error {
	m.mu.RLock()
	reader, backend := m.reader, m.backend
	m.mu.RUnlock()
	if reader == nil || backend == nil {
		return nil
	}
	rows, err := reader.QueryContext(ctx, `SELECT payload FROM memory_facts WHERE app_id=? AND bank_id=? `, AppID, bank)
	if err != nil {
		return err
	}
	var facts []memory.Fact
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			break
		}
		var fact memory.Fact
		if err = json.Unmarshal([]byte(body), &fact); err != nil {
			break
		}
		facts = append(facts, fact)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	invalid := map[string]bool{}
	for _, fact := range facts {
		if !m.operationalFactValid(ctx, bank, fact) {
			invalid[fact.DocumentID] = true
		}
	}
	for document := range invalid {
		if m.store.memoryForgotten(ctx, bank, document) || !strings.HasPrefix(document, "episode/") {
			if err := backend.Forget(ctx, scope(bank), document); err != nil {
				return err
			}
		} else {
			if err := backend.InvalidateDocument(ctx, scope(bank), document); err != nil {
				return err
			}
			_, err := m.store.db.ExecContext(ctx, `UPDATE task_episodes SET status='queued',facts=0,attempts=0,retry_at=NULL,error='' WHERE turn_id=? AND forgotten=0`, strings.TrimPrefix(document, "episode/"))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *MemoryService) consolidateBank(ctx context.Context, bank string) (memory.ConsolidationResult, error) {
	m.workMu.Lock()
	defer m.workMu.Unlock()
	ctx, stop := m.learningContext(ctx)
	defer stop()
	if !validBank(bank) {
		return memory.ConsolidationResult{}, errors.New("unknown memory bank")
	}
	if err := m.pruneInvalidOperationalFacts(ctx, bank); err != nil {
		return memory.ConsolidationResult{}, err
	}
	m.mu.RLock()
	backend := m.backend
	m.mu.RUnlock()
	if err := m.archiveLessons(ctx, bank); err != nil {
		return memory.ConsolidationResult{}, err
	}
	if backend == nil {
		return memory.ConsolidationResult{}, errors.New("memory is not available")
	}
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	result, err := backend.ConsolidateMemory(callCtx, scope(bank), 25)
	for batch := 12; err != nil && strings.Contains(err.Error(), "model packet budget") && batch >= 1; batch = max(0, batch/2) {
		result, err = backend.ConsolidateMemory(callCtx, scope(bank), batch)
	}
	if err == nil {
		err = m.archiveLessons(ctx, bank)
	}
	state, _ := m.store.maintenance(context.WithoutCancel(ctx), bank)
	var last, retry any
	attempts := 0
	message := ""
	if err == nil {
		last = time.Now().UTC()
	} else {
		attempts = state.Attempts + 1
		if ctx.Err() != nil {
			attempts = state.Attempts
		}
		message = excerpt(redactSecrets(err.Error()), 1000)
		if state.LastSuccess != nil {
			last = *state.LastSuccess
		}
		if attempts < 3 {
			retry = time.Now().UTC().Add(time.Duration(attempts*attempts) * time.Minute)
		}
	}
	body, _ := json.Marshal(result)
	_, saveErr := m.store.db.ExecContext(context.WithoutCancel(ctx), `INSERT INTO memory_maintenance(bank,last_success,retry_at,attempts,error,result) VALUES(?,?,?,?,?,?)
		ON CONFLICT(bank) DO UPDATE SET last_success=excluded.last_success,retry_at=excluded.retry_at,attempts=excluded.attempts,error=excluded.error,result=excluded.result`, bank, last, retry, attempts, message, string(body))
	if err == nil && saveErr != nil {
		return result, saveErr
	}
	if err == nil && result.Created+result.Updated+result.Deleted > 0 && m.host != nil {
		if botID, ok := strings.CutPrefix(bank, "bot:"); ok {
			bot, _ := m.store.GetBot(ctx, botID)
			if bot != nil && !bot.RetroOff {
				_ = m.store.QueueRetrospective(ctx, botID, "operational memory was consolidated")
				m.host.WakeRetro()
			}
		}
	}
	return result, err
}

type OperationalLesson struct {
	memory.Observation
	State         string   `json:"state"`
	TaskCount     int      `json:"task_count"`
	VerifiedTasks int      `json:"verified_tasks"`
	SourceIDs     []string `json:"source_ids"`
}

func (m *MemoryService) Lessons(ctx context.Context, bank string) ([]OperationalLesson, error) {
	m.mu.RLock()
	reader := m.reader
	m.mu.RUnlock()
	if reader == nil {
		return []OperationalLesson{}, nil
	}
	rows, err := reader.QueryContext(ctx, `SELECT payload FROM memory_observations WHERE app_id=? AND bank_id=? ORDER BY rowid DESC LIMIT 100`, AppID, bank)
	if err != nil {
		return nil, err
	}
	var observations []memory.Observation
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			break
		}
		var item memory.Observation
		if err = json.Unmarshal([]byte(raw), &item); err != nil {
			break
		}
		observations = append(observations, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []OperationalLesson{}
	for _, observation := range observations {
		tasks, verified := map[string]bool{}, map[string]bool{}
		correction, valid := false, true
		sourceIDs := map[string]bool{}
		kinds := map[string]bool{}
		for _, id := range observation.SourceFactIDs {
			var raw string
			if reader.QueryRowContext(ctx, `SELECT payload FROM memory_facts WHERE app_id=? AND bank_id=? AND id=?`, AppID, bank, id).Scan(&raw) != nil {
				valid = false
				break
			}
			var fact memory.Fact
			if json.Unmarshal([]byte(raw), &fact) != nil || !m.operationalFactValid(ctx, bank, fact) {
				valid = false
				break
			}
			sourceIDs[fact.DocumentID] = true
			kinds[fact.Metadata["evidence_kind"]] = true
			if task := fact.Metadata["task_id"]; task != "" {
				tasks[task] = true
				if fact.Metadata["verified"] == "true" {
					verified[task] = true
				}
				if fact.Metadata["evidence_kind"] == "correction" {
					correction = true
				}
			}
		}
		if !valid {
			continue
		}
		state := "candidate"
		if len(verified) >= 2 || correction {
			state = "supported"
		}
		if observation.IsStale {
			state = "stale"
		}
		// Mixed outcomes need narrowed advice before being treated as a procedure.
		if kinds["failure"] && (kinds["success"] || kinds["outcome"]) && !correction {
			state = "mixed evidence"
		}
		if !correction && time.Since(observation.MentionedAt) > 30*24*time.Hour && state == "candidate" {
			state = "expired incident"
		}
		refs := []string{}
		for id := range sourceIDs {
			refs = append(refs, id)
		}
		out = append(out, OperationalLesson{Observation: observation, State: state, TaskCount: len(tasks), VerifiedTasks: len(verified), SourceIDs: refs})
	}
	return out, nil
}

func (m *MemoryService) learningReady(ctx context.Context) bool {
	if m.host == nil {
		return true
	}
	var running int
	if m.store.db.QueryRowContext(ctx, `SELECT count(*) FROM retrospectives WHERE status='running'`).Scan(&running) != nil || running > 0 {
		return false
	}
	settings, err := m.settings.Get(ctx)
	var working int
	if m.store.db.QueryRowContext(ctx, `SELECT count(*) FROM turns WHERE status='working'`).Scan(&working) != nil {
		return false
	}
	return err == nil && (settings.Memory.Provider != ProviderLocal || m.host.ActiveRuns() == 0 && working == 0)
}

func (m *MemoryService) invalidateForgottenProposals(ctx context.Context, bank string) {
	if m.host == nil {
		return
	}
	proposals, err := m.store.PendingProposals(ctx)
	if err != nil {
		return
	}
	for _, proposal := range proposals {
		bot, err := m.store.GetBot(ctx, proposal.BotID)
		if err != nil || bot.MemoryBank() != bank {
			continue
		}
		var proof ProposalEvidence
		_ = json.Unmarshal(proposal.Evidence, &proof)
		for _, source := range proof.Sources {
			if _, err := m.store.ReadHistory(ctx, bank, HistoryRead{SourceID: source, Limit: 1}); err != nil {
				_, _ = m.store.db.ExecContext(ctx, `DELETE FROM instruction_proposals WHERE id=?`, proposal.ID)
				break
			}
		}
	}
}

func (m *MemoryService) learningContext(ctx context.Context) (context.Context, context.CancelFunc) {
	child, cancel := context.WithCancel(ctx)
	m.cancelMu.Lock()
	m.learningCancel = cancel
	m.cancelMu.Unlock()
	return child, func() { cancel(); m.cancelMu.Lock(); m.learningCancel = nil; m.cancelMu.Unlock() }
}
func (m *MemoryService) yieldToChat() {
	m.cancelMu.Lock()
	defer m.cancelMu.Unlock()
	if m.learningCancel != nil {
		m.learningCancel()
	}
}

func (m *MemoryService) idleLearningReady(ctx context.Context) bool {
	return m.host != nil && m.host.ActiveRuns() == 0 && m.host.IdleFor() > 3*time.Minute && m.learningReady(ctx)
}
