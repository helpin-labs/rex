package rex

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/helpin-ai/agent-runtime/memory"
)

func (m *MemoryService) archiveLessons(ctx context.Context, bank string) error {
	m.mu.RLock()
	reader := m.reader
	m.mu.RUnlock()
	if reader == nil {
		return nil
	}
	rows, err := reader.QueryContext(ctx, `SELECT payload FROM memory_observations WHERE app_id=? AND bank_id=?`, AppID, bank)
	if err != nil {
		return err
	}
	observations := []memory.Observation{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			break
		}
		var observation memory.Observation
		if err = json.Unmarshal([]byte(raw), &observation); err != nil {
			break
		}
		observations = append(observations, observation)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, observation := range observations {
		if len(observation.SourceFactIDs) == 0 {
			continue
		}
		sources := map[string]bool{}
		valid := true
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
			sources[fact.DocumentID] = true
		}
		if !valid {
			continue
		}
		refs := []string{}
		for source := range sources {
			refs = append(refs, source)
		}
		payload, _ := json.Marshal(observation)
		digest := sha256.Sum256(payload)
		version := hex.EncodeToString(digest[:])
		sourceJSON, _ := json.Marshal(refs)
		if _, err := m.store.db.ExecContext(ctx, `INSERT INTO lesson_versions(bank,observation_id,version,payload,source_ids,recorded_at) VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`, bank, observation.ID, version, string(payload), string(sourceJSON), time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) readLessonVersion(ctx context.Context, bank, sourceID string) (HistoryEntry, error) {
	parts := strings.Split(strings.TrimPrefix(sourceID, "lesson/"), "/")
	if len(parts) < 1 || len(parts) > 2 || parts[0] == "" {
		return HistoryEntry{}, ErrNotFound
	}
	query := `SELECT version,payload,source_ids,recorded_at FROM lesson_versions WHERE bank=? AND observation_id=?`
	args := []any{bank, parts[0]}
	if len(parts) == 2 {
		query += ` AND version=?`
		args = append(args, parts[1])
	}
	query += ` ORDER BY recorded_at DESC LIMIT 1`
	var version, payload, sourceJSON string
	var at time.Time
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&version, &payload, &sourceJSON, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return HistoryEntry{}, ErrNotFound
	}
	if err != nil {
		return HistoryEntry{}, err
	}
	var sources []string
	_ = json.Unmarshal([]byte(sourceJSON), &sources)
	for _, source := range sources {
		if strings.HasPrefix(source, "episode/") {
			e, err := s.Episode(ctx, strings.TrimPrefix(source, "episode/"))
			if err != nil || e.Bank != bank || e.Forgotten {
				return HistoryEntry{}, ErrNotFound
			}
		}
		if s.memoryForgotten(ctx, bank, source) {
			return HistoryEntry{}, ErrNotFound
		}
	}
	body, _ := json.Marshal(map[string]any{"lesson": json.RawMessage(payload), "source_ids": sources, "version": version})
	return HistoryEntry{ID: "lesson/" + parts[0] + "/" + version, Kind: "lesson_version", Text: string(body), CreatedAt: at}, nil
}
