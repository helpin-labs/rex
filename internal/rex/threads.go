package rex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// Each bot has one ongoing conversation, its thread. Long threads stay
// workable because the runtime compacts a run's context and durable facts
// live in memory. "Start fresh" begins a new section: earlier turns stay
// visible, but the bot no longer carries them as context.

// FreshStartSection labels the first turn after "Start fresh".
const FreshStartSection = "Fresh start"

// ThreadForBot returns the bot's conversation, creating it when needed.
func (s *Store) ThreadForBot(ctx context.Context, bot Bot) (*Conversation, error) {
	item, err := scanConversation(s.db.QueryRowContext(ctx, `SELECT `+conversationColumns+` FROM conversations WHERE bot_id=? ORDER BY updated_at DESC LIMIT 1`, bot.ID))
	if err == nil {
		return item, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return s.CreateConversation(ctx, bot.ID, bot.Name, "chat")
}

// MergeThreads gives every bot a single conversation. A bot with several
// keeps its most recently used one, so its newest runs keep their
// conversation; the turns and runs of the others move into it in time
// order, and each merged conversation's first turn carries its title as a
// section. Rex backs up its database first when anything is merged.
func (s *Store) MergeThreads(ctx context.Context, backupDir string) error {
	bots, err := s.ListBots(ctx)
	if err != nil {
		return err
	}
	backedUp := false
	for _, bot := range bots {
		conversations, err := s.ListConversations(ctx, bot.ID) // newest first
		if err != nil {
			return err
		}
		if len(conversations) == 0 {
			if _, err := s.CreateConversation(ctx, bot.ID, bot.Name, "chat"); err != nil {
				return err
			}
			continue
		}
		if len(conversations) == 1 {
			continue
		}
		if !backedUp && backupDir != "" {
			path := filepath.Join(backupDir, "rex.db."+time.Now().Format("20060102-150405")+".before-threads.bak")
			if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
				return fmt.Errorf("back up before merging conversations: %w", err)
			}
			backedUp = true
			slog.Info("merging conversations into one per bot", "backup", path)
		}
		keeper := conversations[0]
		err = withTx(ctx, s.db, func(tx *sql.Tx) error {
			for _, conversation := range conversations {
				// Mark where each earlier conversation began.
				if _, err := tx.ExecContext(ctx, `UPDATE turns SET section=? WHERE id=(SELECT id FROM turns WHERE conversation_id=? ORDER BY started_at LIMIT 1) AND section=''`, conversation.Title, conversation.ID); err != nil {
					return err
				}
				if conversation.ID == keeper.ID {
					continue
				}
				if _, err := tx.ExecContext(ctx, `UPDATE turns SET conversation_id=? WHERE conversation_id=?`, keeper.ID, conversation.ID); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE conversation_runs SET conversation_id=? WHERE conversation_id=?`, keeper.ID, conversation.ID); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE routines SET conversation_id=? WHERE conversation_id=?`, keeper.ID, conversation.ID); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `DELETE FROM conversations WHERE id=?`, conversation.ID); err != nil {
					return err
				}
			}
			// Runs keep their order in time within the merged thread.
			rows, err := tx.QueryContext(ctx, `SELECT run_id FROM conversation_runs WHERE conversation_id=? ORDER BY created_at`, keeper.ID)
			if err != nil {
				return err
			}
			var runs []string
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				runs = append(runs, id)
			}
			rows.Close()
			for position, id := range runs {
				if _, err := tx.ExecContext(ctx, `UPDATE conversation_runs SET position=? WHERE run_id=?`, position+1, id); err != nil {
					return err
				}
			}
			_, err = tx.ExecContext(ctx, `UPDATE conversations SET title=?, kind='chat' WHERE id=?`, bot.Name, keeper.ID)
			return err
		})
		if err != nil {
			return fmt.Errorf("merge conversations of %s: %w", bot.Name, err)
		}
	}
	return nil
}

// StartFresh detaches the bot's run so the next message starts a new one
// without the earlier conversation as context.
func (s *Store) StartFresh(ctx context.Context, conversationID string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `UPDATE conversations SET active_run_id=NULL, context_start=?, updated_at=? WHERE id=?`, now, now, conversationID)
	return err
}

// ContextRunIDs lists the runs whose transcript a new run carries forward:
// the latest few since the last fresh start.
func (s *Store) ContextRunIDs(ctx context.Context, conversation *Conversation) ([]string, error) {
	since := time.Time{}
	if conversation.ContextStart != nil {
		since = *conversation.ContextStart
	}
	ids, err := s.strings(ctx, `SELECT run_id FROM conversation_runs WHERE conversation_id=? AND created_at>=? ORDER BY position DESC LIMIT 4`, conversation.ID, since)
	if err != nil {
		return nil, err
	}
	for left, right := 0, len(ids)-1; left < right; left, right = left+1, right-1 {
		ids[left], ids[right] = ids[right], ids[left]
	}
	return ids, nil
}

// DetachRun makes the conversation's next message start a new run that
// carries the conversation forward.
func (s *Store) DetachRun(ctx context.Context, conversationID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE conversations SET active_run_id=NULL WHERE id=?`, conversationID)
	return err
}

func (s *Store) SetRunFingerprint(ctx context.Context, conversationID, fingerprint string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE conversations SET run_fingerprint=? WHERE id=?`, fingerprint, conversationID)
	return err
}

// ThreadSummary is a bot's conversation with its latest message, for the
// bot list.
type ThreadSummary struct {
	Conversation
	Preview     string     `json:"preview"`
	PreviewFrom string     `json:"preview_from,omitempty"` // user or bot
	LastAt      *time.Time `json:"last_at,omitempty"`
	Working     bool       `json:"working"`
}

func (s *Store) ThreadSummaries(ctx context.Context) ([]ThreadSummary, error) {
	conversations, err := s.ListConversations(ctx, "")
	if err != nil {
		return nil, err
	}
	items := make([]ThreadSummary, 0, len(conversations))
	for _, conversation := range conversations {
		item := ThreadSummary{Conversation: conversation}
		if turn, err := s.LatestTurnForConversation(ctx, conversation.ID); err == nil {
			at := turn.StartedAt
			if turn.SettledAt != nil {
				at = *turn.SettledAt
			}
			item.LastAt = &at
			item.Working = turn.Status == "working"
			item.Preview, item.PreviewFrom = turn.UserText, "user"
			if turn.AssistantText != "" && turn.Status != "working" {
				item.Preview, item.PreviewFrom = turn.AssistantText, "bot"
			}
			item.Preview = previewText(item.Preview)
		}
		items = append(items, item)
	}
	return items, nil
}

// markdownMarks are stripped from previews so the bot list reads as text.
var markdownMarks = strings.NewReplacer("**", "", "__", "", "`", "", "# ", "", "## ", "", "### ", "", "> ", "")

func previewText(text string) string {
	runes := []rune(strings.Join(strings.Fields(markdownMarks.Replace(text)), " "))
	if len(runes) > 140 {
		return string(runes[:139]) + "…"
	}
	return string(runes)
}

func (h *Host) startFresh(w http.ResponseWriter, r *http.Request) {
	conversation, err := h.Store.GetConversation(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if conversation.ActiveRunID != nil {
		if run, err := h.Runtime.GetRun(r.Context(), *conversation.ActiveRunID); err == nil && (run.Status == "running" || run.Status == "queued") {
			writeError(w, http.StatusConflict, errors.New("the bot is still working; stop it or wait before starting fresh"))
			return
		}
	}
	if err := h.Store.StartFresh(r.Context(), conversation.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	updated, err := h.Store.GetConversation(r.Context(), conversation.ID)
	writeResult(w, updated, err)
}

// Forgetting starts new runs so compacted checkpoints cannot reintroduce a source.
func (h *Host) invalidateBankContext(ctx context.Context, bank string) error {
	bots, err := h.Store.ListBots(ctx)
	if err != nil {
		return err
	}
	for _, bot := range bots {
		if bot.MemoryBank() != bank {
			continue
		}
		conversations, err := h.Store.ListConversations(ctx, bot.ID)
		if err != nil {
			return err
		}
		for _, conversation := range conversations {
			if conversation.ActiveRunID != nil {
				if run, err := h.Runtime.GetRun(ctx, *conversation.ActiveRunID); err == nil && !run.Terminal() {
					if _, err := h.Runtime.CancelRun(ctx, run.ID); err != nil {
						return err
					}
				}
			}
			if err := h.Store.StartFresh(ctx, conversation.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
