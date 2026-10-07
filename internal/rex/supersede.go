package rex

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Conflicting memories: recall returns every matching fact, so after a user
// changes their mind both the old and the new preference come back. When a
// source is retained, Rex compares each new fact with similar older facts
// and asks the extraction model whether the new one replaces the old one.
// Replaced facts are hidden from recall; forgetting the newer source makes
// them current again.

const maxSupersedeChecks = 4

func (m *MemoryService) resolveConflicts(ctx context.Context, bank, documentID string) {
	m.mu.RLock()
	pool := m.pool
	m.mu.RUnlock()
	if pool == nil {
		return
	}
	extractor := pool.forContext(ctx)
	facts := m.documentFacts(ctx, bank, documentID)
	checks := 0
	for _, fact := range facts {
		candidates, err := m.Recall(ctx, bank, fact.Text, 6)
		if err != nil {
			continue
		}
		for _, old := range candidates {
			if old.DocumentID == documentID || old.MentionedAt.After(fact.MentionedAt) || checks >= maxSupersedeChecks {
				continue
			}
			checks++
			replaces, err := extractor.Replaces(ctx, displayFact(old.Text), displayFact(fact.Text))
			if err != nil {
				slog.Warn("memory conflict check failed", "error", err)
				continue
			}
			if replaces {
				slog.Info("memory replaced by a newer fact", "old", displayFact(old.Text), "new", displayFact(fact.Text))
				_ = m.store.AddSuperseded(ctx, bank, old.ID, fact.ID, documentID)
			}
		}
	}
}

func (m *MemoryService) documentFacts(ctx context.Context, bank, documentID string) []MemoryFact {
	m.mu.RLock()
	reader := m.reader
	m.mu.RUnlock()
	if reader == nil {
		return nil
	}
	rows, err := reader.QueryContext(ctx, `SELECT payload FROM memory_facts WHERE app_id=? AND bank_id=? AND document_id=?`, AppID, bank, documentID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var facts []MemoryFact
	for rows.Next() {
		var payload string
		if rows.Scan(&payload) != nil {
			continue
		}
		var fact struct {
			ID          string    `json:"id"`
			Text        string    `json:"text"`
			MentionedAt time.Time `json:"mentioned_at"`
		}
		if json.Unmarshal([]byte(payload), &fact) == nil {
			facts = append(facts, MemoryFact{ID: fact.ID, Text: fact.Text, MentionedAt: fact.MentionedAt})
		}
	}
	return facts
}

// displayFact drops the "| Involving: …" style dimension labels that the
// memory store appends to fact text.
func displayFact(text string) string {
	text = strings.TrimSpace(text)
	if cut := strings.Index(text, " | "); cut > 0 {
		text = text[:cut]
	}
	return text
}

const replacesPrompt = `You maintain a personal assistant's memory. Decide whether a NEW memory replaces an OLD one: the user changed their mind, corrected it, or the old one is no longer true because of the new one. Memories that are merely related, or that can both be true, do not replace each other. Answer with exactly one word: yes or no.`

// Replaces asks the extraction model whether newer replaces older.
func (e *FactExtractor) Replaces(ctx context.Context, older, newer string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	body := map[string]any{
		"model": e.model,
		"messages": []map[string]string{
			{"role": "system", "content": replacesPrompt},
			{"role": "user", "content": "OLD: " + older + "\nNEW: " + newer + "\nDoes NEW replace OLD?"},
		},
	}
	if e.local {
		opts := extractionOptions(ProviderLocal)
		for key, value := range opts {
			body[key] = value
		}
	}
	headers := map[string]string{}
	if e.apiKey != "" {
		headers["Authorization"] = "Bearer " + e.apiKey
	}
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := httpJSON(ctx, http.MethodPost, e.baseURL+"/chat/completions", headers, body, &response); err != nil {
		return false, err
	}
	if len(response.Choices) == 0 {
		return false, nil
	}
	answer := strings.ToLower(strings.TrimSpace(response.Choices[0].Message.Content))
	return strings.HasPrefix(answer, "yes"), nil
}

// --- store ---

func (s *Store) AddSuperseded(ctx context.Context, bank, factID, byFactID, byDocument string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO memory_superseded(bank,fact_id,by_fact_id,by_document,created_at) VALUES(?,?,?,?,?) ON CONFLICT(bank,fact_id) DO UPDATE SET by_fact_id=excluded.by_fact_id, by_document=excluded.by_document`, bank, factID, byFactID, byDocument, time.Now().UTC())
	return err
}

func (s *Store) SupersededFacts(ctx context.Context, bank string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT fact_id, by_fact_id FROM memory_superseded WHERE bank=?`, bank)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var fact, by string
		if rows.Scan(&fact, &by) == nil {
			out[fact] = by
		}
	}
	return out, rows.Err()
}

func (s *Store) ClearSupersededBy(ctx context.Context, bank, documentID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM memory_superseded WHERE bank=? AND by_document=?`, bank, documentID)
	return err
}
