package rex

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type TaskEpisode struct {
	DurationMS       int64      `json:"duration_ms"`
	ExtractionSchema string     `json:"extraction_schema,omitempty"`
	TurnID           string     `json:"turn_id"`
	BotID            string     `json:"bot_id"`
	Bank             string     `json:"bank"`
	RunID            string     `json:"run_id"`
	Status           string     `json:"status"`
	Attempts         int        `json:"attempts"`
	RetryAt          *time.Time `json:"retry_at,omitempty"`
	Error            string     `json:"error,omitempty"`
	Facts            int        `json:"facts"`
	Revision         string     `json:"revision"`
	Forgotten        bool       `json:"forgotten"`
	Model            string     `json:"model,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type HistoryEntry struct {
	ID        string    `json:"source_id"`
	TurnID    string    `json:"turn_id"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text"`
	Sequence  int64     `json:"sequence"`
	CreatedAt time.Time `json:"created_at"`
	Truncated bool      `json:"truncated,omitempty"`
}

const episodeColumns = `turn_id,bot_id,bank,run_id,status,attempts,retry_at,error,facts,revision,forgotten,model,created_at,updated_at,duration_ms,extraction_schema`

func episodeDocument(turnID string) string { return "episode/" + turnID }

// Capture jobs are independent of the model and survive extraction failures.
func (s *Store) QueueEpisode(ctx context.Context, turn Turn) error {
	bank := firstNonEmpty(turn.MemoryBank, "bot:"+turn.BotID)
	_, err := s.db.ExecContext(ctx, `INSERT INTO task_episodes(turn_id,bot_id,bank,run_id,forgotten,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?) ON CONFLICT(turn_id) DO UPDATE SET run_id=excluded.run_id,status=CASE WHEN task_episodes.status='captured' THEN 'capture' ELSE task_episodes.status END`, turn.ID, turn.BotID, bank, turn.RunID, turn.MemoryStatus == "forgotten", turn.StartedAt, time.Now().UTC())
	return err
}

func scanEpisode(row interface{ Scan(...any) error }) (TaskEpisode, error) {
	var e TaskEpisode
	var retry sql.NullTime
	err := row.Scan(&e.TurnID, &e.BotID, &e.Bank, &e.RunID, &e.Status, &e.Attempts, &retry, &e.Error, &e.Facts, &e.Revision, &e.Forgotten, &e.Model, &e.CreatedAt, &e.UpdatedAt, &e.DurationMS, &e.ExtractionSchema)
	if retry.Valid {
		e.RetryAt = &retry.Time
	}
	return e, err
}

func (s *Store) Episode(ctx context.Context, turnID string) (TaskEpisode, error) {
	e, err := scanEpisode(s.db.QueryRowContext(ctx, `SELECT `+episodeColumns+` FROM task_episodes WHERE turn_id=?`, turnID))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return e, err
}

func (s *Store) episodeQueue(ctx context.Context, status string) (*TaskEpisode, error) {
	e, err := scanEpisode(s.db.QueryRowContext(ctx, `SELECT `+episodeColumns+` FROM task_episodes
		WHERE (status=? OR ?='capture' AND status='captured' AND updated_at<=?)
		AND (?<>'queued' OR EXISTS(SELECT 1 FROM turns t WHERE t.id=task_episodes.turn_id AND t.status<>'working'))
		AND forgotten=0 AND (retry_at IS NULL OR retry_at<=?) ORDER BY updated_at,created_at LIMIT 1`, status, status, time.Now().UTC().Add(-30*time.Second), status, time.Now().UTC()))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &e, err
}

// User-facing tasks are archived independently of learning; helpers have no turns.
func (s *Store) reconcileEpisodes(ctx context.Context, limit int) error {
	turns, err := s.turns(ctx, `SELECT `+turnColumns+` FROM turns WHERE status IN ('working','settled','failed','cancelled') AND (status<>'working' OR run_id<>'')
		AND NOT EXISTS(SELECT 1 FROM task_episodes e WHERE e.turn_id=turns.id) ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return err
	}
	for _, turn := range turns {
		if err = s.QueueEpisode(ctx, turn); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) saveHistory(ctx context.Context, e TaskEpisode, entries []HistoryEntry) error {
	body, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(body)
	revision := hex.EncodeToString(digest[:])
	return withTx(ctx, s.db, func(tx *sql.Tx) error {
		var forgotten bool
		var oldRevision, status, taskStatus string
		if err := tx.QueryRowContext(ctx, `SELECT e.forgotten,e.revision,e.status,t.status FROM task_episodes e JOIN turns t ON t.id=e.turn_id WHERE e.turn_id=? AND e.bank=?`, e.TurnID, e.Bank).Scan(&forgotten, &oldRevision, &status, &taskStatus); err != nil {
			return err
		}
		if forgotten {
			return errors.New("source was forgotten")
		}
		nextStatus := "queued"
		if taskStatus == "working" {
			nextStatus = "captured"
		} else if e.Status == "captured" {
			// The turn settled while its working snapshot was being fetched.
			nextStatus = "capture"
		}
		if oldRevision == revision {
			if nextStatus == "captured" || nextStatus == "capture" || status == "captured" || status == "capture" {
				_, err := tx.ExecContext(ctx, `UPDATE task_episodes SET status=?,updated_at=? WHERE turn_id=?`, nextStatus, time.Now().UTC(), e.TurnID)
				return err
			}
			return nil
		}
		if oldRevision != "" {
			if _, err := tx.ExecContext(ctx, `DELETE FROM lesson_versions WHERE bank=? AND EXISTS(SELECT 1 FROM json_each(lesson_versions.source_ids) WHERE value=?)`, e.Bank, episodeDocument(e.TurnID)); err != nil {
				return err
			}
		}

		existing := map[string]HistoryEntry{}
		rows, err := tx.QueryContext(ctx, `SELECT id,kind,text,sequence_no,created_at FROM task_history WHERE turn_id=?`, e.TurnID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var entry HistoryEntry
			if err := rows.Scan(&entry.ID, &entry.Kind, &entry.Text, &entry.Sequence, &entry.CreatedAt); err != nil {
				rows.Close()
				return err
			}
			existing[entry.ID] = entry
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		seen := map[string]bool{}
		for _, entry := range entries {
			if entry.ID == "" || entry.TurnID != e.TurnID {
				return errors.New("invalid source identity")
			}
			if seen[entry.ID] {
				return errors.New("duplicate source identity")
			}
			seen[entry.ID] = true
			previous, exists := existing[entry.ID]
			delete(existing, entry.ID)
			if exists && previous.Text == entry.Text && previous.Kind == entry.Kind && previous.Sequence == entry.Sequence && previous.CreatedAt.Equal(entry.CreatedAt) {
				continue
			}
			result, err := tx.ExecContext(ctx, `INSERT INTO task_history(id,turn_id,kind,text,sequence_no,created_at) VALUES(?,?,?,?,?,?)
				ON CONFLICT(id) DO UPDATE SET text=excluded.text,kind=excluded.kind,sequence_no=excluded.sequence_no,created_at=excluded.created_at WHERE task_history.turn_id=excluded.turn_id`, entry.ID, e.TurnID, entry.Kind, entry.Text, entry.Sequence, entry.CreatedAt)
			if err != nil {
				return err
			}
			changed, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if changed != 1 {
				return errors.New("source belongs to another task")
			}
		}
		for id := range existing {
			if _, err := tx.ExecContext(ctx, `DELETE FROM task_history WHERE turn_id=? AND id=?`, e.TurnID, id); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE task_episodes SET status=?,revision=?,attempts=0,retry_at=NULL,error='',updated_at=? WHERE turn_id=?`, nextStatus, revision, time.Now().UTC(), e.TurnID)
		return err
	})
}

func (s *Store) episodeFailed(ctx context.Context, e TaskEpisode, stage string, err error) error {
	status := stage
	var retry any
	attempts := e.Attempts + 1
	if attempts >= 3 {
		status = "failed"
	} else {
		retry = time.Now().UTC().Add(time.Duration(attempts*attempts) * time.Minute)
	}
	_, saveErr := s.db.ExecContext(ctx, `UPDATE task_episodes SET status=?,attempts=?,retry_at=?,error=?,updated_at=? WHERE turn_id=? AND revision=? AND forgotten=0`, status, attempts, retry, excerpt(redactSecrets(err.Error()), 1000), time.Now().UTC(), e.TurnID, e.Revision)
	return saveErr
}

func (s *Store) historyEntries(ctx context.Context, bank, turnID string) ([]HistoryEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT h.id,h.turn_id,h.kind,h.text,h.sequence_no,h.created_at FROM task_history h JOIN task_episodes e ON e.turn_id=h.turn_id WHERE e.bank=? AND e.turn_id=? AND e.forgotten=0 ORDER BY h.sequence_no,h.created_at,h.id`, bank, turnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryEntry
	for rows.Next() {
		var item HistoryEntry
		if err := rows.Scan(&item.ID, &item.TurnID, &item.Kind, &item.Text, &item.Sequence, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

type HistorySearch struct {
	Query          string `json:"query"`
	ConversationID string `json:"conversation_id,omitempty"`
	After          string `json:"after,omitempty"`
	Before         string `json:"before,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	Cursor         int    `json:"cursor,omitempty"`
}
type HistoryPage struct {
	Entries    []HistoryEntry `json:"entries"`
	NextCursor *int           `json:"next_cursor,omitempty"`
}

func historyQuery(text string) string {
	words := strings.Fields(text)
	if len(words) > 20 {
		words = words[:20]
	}
	for i, word := range words {
		words[i] = `"` + strings.ReplaceAll(word, `"`, `""`) + `"`
	}
	return strings.Join(words, " OR ")
}

func (s *Store) SearchHistory(ctx context.Context, bank string, request HistorySearch) (HistoryPage, error) {
	if !validBank(bank) || len(request.Query) > 2000 || request.Cursor < 0 || request.Cursor > 1000000 {
		return HistoryPage{}, errors.New("invalid history query")
	}
	limit := request.Limit
	if limit == 0 {
		limit = 10
	}
	if limit < 1 || limit > 20 {
		return HistoryPage{}, errors.New("history limit must be 1–20")
	}
	query := `SELECT h.id,h.turn_id,h.kind,h.text,h.sequence_no,h.created_at FROM task_history h JOIN task_episodes e ON e.turn_id=h.turn_id JOIN turns t ON t.id=e.turn_id`
	args := []any{}
	if strings.TrimSpace(request.Query) != "" {
		query += ` JOIN task_history_fts f ON f.rowid=h.rowid`
	}
	query += ` WHERE e.bank=? AND e.forgotten=0`
	args = append(args, bank)
	if strings.TrimSpace(request.Query) != "" {
		query += ` AND task_history_fts MATCH ?`
		args = append(args, historyQuery(request.Query))
	}
	if request.ConversationID != "" {
		query += ` AND t.conversation_id=?`
		args = append(args, request.ConversationID)
	}
	for _, bound := range []struct{ value, op string }{{request.After, ">="}, {request.Before, "<="}} {
		if bound.value != "" {
			when, err := time.Parse(time.RFC3339, bound.value)
			if err != nil {
				return HistoryPage{}, errors.New("history dates must use RFC3339")
			}
			query += ` AND h.created_at` + bound.op + `?`
			args = append(args, when.UTC())
		}
	}
	query += ` ORDER BY h.created_at DESC,h.id LIMIT ? OFFSET ?`
	args = append(args, limit+1, request.Cursor)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return HistoryPage{}, err
	}
	defer rows.Close()
	page := HistoryPage{Entries: []HistoryEntry{}}
	for rows.Next() {
		var entry HistoryEntry
		if err := rows.Scan(&entry.ID, &entry.TurnID, &entry.Kind, &entry.Text, &entry.Sequence, &entry.CreatedAt); err != nil {
			return page, err
		}
		entry.Truncated = len([]rune(entry.Text)) > 600
		entry.Text = excerpt(entry.Text, 600)
		page.Entries = append(page.Entries, entry)
	}
	if len(page.Entries) > limit {
		page.Entries = page.Entries[:limit]
		next := request.Cursor + limit
		page.NextCursor = &next
	}
	return page, rows.Err()
}

type HistoryRead struct {
	SourceID string `json:"source_id"`
	Offset   int    `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}
type HistoryExcerpt struct {
	HistoryEntry
	Offset     int  `json:"offset"`
	NextOffset *int `json:"next_offset,omitempty"`
	Truncated  bool `json:"truncated"`
}

func (s *Store) ReadHistory(ctx context.Context, bank string, request HistoryRead) (HistoryExcerpt, error) {
	limit := request.Limit
	if limit == 0 {
		limit = 12000
	}
	if !validBank(bank) || request.Offset < 0 || limit < 1 || limit > 16000 {
		return HistoryExcerpt{}, errors.New("invalid history range")
	}
	var entry HistoryEntry
	var err error
	if strings.HasPrefix(request.SourceID, "lesson/") {
		entry, err = s.readLessonVersion(ctx, bank, request.SourceID)
	} else if strings.HasPrefix(request.SourceID, "note/") {
		entry.ID = request.SourceID
		entry.Kind = "explicit_note"
		err = s.db.QueryRowContext(ctx, `SELECT content,created_at FROM memory_notes WHERE bank=? AND id=? AND status<>'forgotten'`, bank, strings.TrimPrefix(request.SourceID, "note/")).Scan(&entry.Text, &entry.CreatedAt)
		if s.memoryForgotten(ctx, bank, request.SourceID) {
			return HistoryExcerpt{}, ErrNotFound
		}
		entry.Text = redactSecrets(entry.Text)
	} else if strings.HasPrefix(request.SourceID, "episode/") {
		id := strings.TrimPrefix(request.SourceID, "episode/")
		episode, readErr := s.Episode(ctx, id)
		if readErr != nil || episode.Bank != bank || episode.Forgotten {
			return HistoryExcerpt{}, ErrNotFound
		}
		entries, readErr := s.historyEntries(ctx, bank, id)
		if readErr != nil {
			return HistoryExcerpt{}, readErr
		}
		body, _ := json.Marshal(map[string]any{"episode": episode, "entries": entries})
		entry = HistoryEntry{ID: request.SourceID, TurnID: id, Kind: "episode", Text: string(body), CreatedAt: episode.CreatedAt}
	} else {
		err = s.db.QueryRowContext(ctx, `SELECT h.id,h.turn_id,h.kind,h.text,h.sequence_no,h.created_at FROM task_history h JOIN task_episodes e ON e.turn_id=h.turn_id WHERE e.bank=? AND e.forgotten=0 AND h.id=?`, bank, request.SourceID).Scan(&entry.ID, &entry.TurnID, &entry.Kind, &entry.Text, &entry.Sequence, &entry.CreatedAt)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return HistoryExcerpt{}, ErrNotFound
	}
	if err != nil {
		return HistoryExcerpt{}, err
	}
	text := []rune(entry.Text)
	if request.Offset > len(text) {
		return HistoryExcerpt{}, errors.New("history offset exceeds source length")
	}
	end := min(len(text), request.Offset+limit)
	entry.Text = string(text[request.Offset:end])
	out := HistoryExcerpt{HistoryEntry: entry, Offset: request.Offset, Truncated: end < len(text)}
	if out.Truncated {
		out.NextOffset = &end
	}
	return out, nil
}

func (s *Store) forgetEpisode(ctx context.Context, bank, document string) error {
	turnID := strings.TrimPrefix(document, "episode/")
	if strings.HasPrefix(document, "turn/") {
		turnID = strings.TrimPrefix(document, "turn/")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE task_episodes SET forgotten=1,status='forgotten',updated_at=? WHERE bank=? AND turn_id=?`, time.Now().UTC(), bank, turnID)
	return err
}

func (s *Store) episodeProgress(ctx context.Context, bank string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status,count(*) FROM task_episodes WHERE bank=? GROUP BY status`, bank)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		counts[status] = count
	}
	return counts, rows.Err()
}

func evidenceText(entries []HistoryEntry, limit int) string {
	var out strings.Builder
	for _, entry := range entries {
		line := fmt.Sprintf("[%s] %s: %s\n", entry.ID, entry.Kind, excerpt(entry.Text, 6000))
		if out.Len()+len(line) > limit {
			break
		}
		out.WriteString(line)
	}
	return out.String()
}

func (s *Store) retryEpisode(ctx context.Context, bank, turnID string) error {
	e, err := s.Episode(ctx, turnID)
	if err != nil || e.Bank != bank || e.Forgotten {
		return ErrNotFound
	}
	status := "queued"
	if e.Revision == "" {
		status = "capture"
	}
	_, err = s.db.ExecContext(ctx, `UPDATE task_episodes SET status=?,attempts=0,retry_at=NULL,error='',updated_at=? WHERE bank=? AND turn_id=? AND forgotten=0 AND status<>'extracting'`, status, time.Now().UTC(), bank, turnID)
	return err
}

func (s *Store) episodesForBank(ctx context.Context, bank string, limit int) ([]TaskEpisode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+episodeColumns+` FROM task_episodes WHERE bank=? ORDER BY created_at DESC LIMIT ?`, bank, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskEpisode{}
	for rows.Next() {
		item, err := scanEpisode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) forgetDocument(ctx context.Context, bank, document string) error {
	_, _ = s.db.ExecContext(ctx, `DELETE FROM lesson_versions WHERE bank=? AND EXISTS(SELECT 1 FROM json_each(lesson_versions.source_ids) WHERE value=?)`, bank, document)
	_, err := s.db.ExecContext(ctx, `INSERT INTO memory_tombstones(bank,document_id,created_at) VALUES(?,?,?) ON CONFLICT(bank,document_id) DO NOTHING`, bank, document, time.Now().UTC())
	return err
}
func (s *Store) memoryForgotten(ctx context.Context, bank, document string) bool {
	var count int
	return s.db.QueryRowContext(ctx, `SELECT count(*) FROM memory_tombstones WHERE bank=? AND document_id=?`, bank, document).Scan(&count) != nil || count > 0
}
