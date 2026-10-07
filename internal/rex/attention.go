package rex

import (
	"context"
	"net/http"
	"time"
)

// Attention lists what is waiting for the user: approvals and questions the
// bots asked, plus routines that finished recently. It is derived from the
// runtime's pending interactions on each request, so nothing can go stale.
// The app uses it for notifications and the Dock badge. A routine that
// stopped to ask something shows up as its question instead.

type AttentionItem struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"` // approval, question, reconnect, routine, missed or proposal
	ConversationID string    `json:"conversation_id"`
	BotID          string    `json:"bot_id"`
	Title          string    `json:"title"`
	Detail         string    `json:"detail,omitempty"`
	At             time.Time `json:"at"`
}

// attentionScope bounds how many recent conversations are checked for
// pending interactions on each poll.
const attentionScope = 50

// recentRoutineWindow is how long a finished routine stays notifiable.
const recentRoutineWindow = 10 * time.Minute

func (h *Host) Attention(ctx context.Context) ([]AttentionItem, error) {
	items := []AttentionItem{}
	rows, err := h.Store.db.QueryContext(ctx, `SELECT id,bot_id,active_run_id FROM conversations WHERE active_run_id IS NOT NULL AND active_run_id<>'' ORDER BY updated_at DESC LIMIT ?`, attentionScope)
	if err != nil {
		return nil, err
	}
	type active struct{ conversation, bot, run string }
	var runs []active
	for rows.Next() {
		var item active
		if err := rows.Scan(&item.conversation, &item.bot, &item.run); err != nil {
			rows.Close()
			return nil, err
		}
		runs = append(runs, item)
	}
	rows.Close()
	if h.RuntimeReady() {
		for _, run := range runs {
			interactions, err := h.Runtime.ListInteractions(ctx, run.run)
			if err != nil {
				continue
			}
			for _, interaction := range interactions {
				if interaction.Status != "pending" {
					continue
				}
				kind := "approval"
				switch interaction.Kind {
				case "request_user_input":
					kind = "question"
				case "authentication":
					// An MCP server needs the user to sign in again.
					kind = "reconnect"
				}
				items = append(items, AttentionItem{ID: interaction.ID, Kind: kind, ConversationID: run.conversation, BotID: run.bot,
					Title: interaction.Title, Detail: interaction.Summary, At: interaction.CreatedAt})
			}
		}
	}

	finished, err := h.Store.db.QueryContext(ctx, `SELECT rr.id,rr.status,rr.error,rr.finished_at,r.title,r.bot_id,r.conversation_id
		FROM routine_runs rr JOIN routines r ON r.id=rr.routine_id
		WHERE rr.finished_at IS NOT NULL AND rr.finished_at>? AND rr.status IN ('succeeded','failed') ORDER BY rr.finished_at`, time.Now().UTC().Add(-recentRoutineWindow))
	if err != nil {
		return nil, err
	}
	defer finished.Close()
	for finished.Next() {
		var item AttentionItem
		var status, message string
		if err := finished.Scan(&item.ID, &status, &message, &item.At, &item.Title, &item.BotID, &item.ConversationID); err != nil {
			return nil, err
		}
		item.Kind = "routine"
		switch status {
		case "failed":
			item.Detail = "Failed: " + message
		default:
			item.Detail = "Finished"
		}
		items = append(items, item)
	}
	if err := finished.Err(); err != nil {
		return nil, err
	}
	finished.Close()

	// Routines that missed their latest slot while the Mac was off wait
	// for the user to run or skip them.
	missed, err := h.Store.db.QueryContext(ctx, `SELECT rr.id,rr.scheduled_for,r.title,r.bot_id,r.conversation_id
		FROM routine_runs rr JOIN routines r ON r.id=rr.routine_id WHERE rr.status='missed' ORDER BY rr.scheduled_for`)
	if err != nil {
		return nil, err
	}
	defer missed.Close()
	for missed.Next() {
		item := AttentionItem{Kind: "missed"}
		if err := missed.Scan(&item.ID, &item.At, &item.Title, &item.BotID, &item.ConversationID); err != nil {
			return nil, err
		}
		item.Detail = "Didn't run at " + item.At.Local().Format("Mon 2 Jan 15:04") + " because the Mac was off or Rex was closed."
		items = append(items, item)
	}
	if err := missed.Err(); err != nil {
		return nil, err
	}

	// Instructions a bot proposed for itself wait for the user to apply.
	proposals, err := h.Store.PendingProposals(ctx)
	if err != nil {
		return nil, err
	}
	for _, proposal := range proposals {
		title := "New instructions"
		switch {
		case proposal.Kind == ProposalInstallSkill:
			title = "Suggested skill"
		case proposal.Source == ProposalFromRetrospective:
			title = "Suggested improvement"
		}
		item := AttentionItem{ID: proposal.ID, Kind: "proposal", BotID: proposal.BotID, Title: title, Detail: proposal.Summary, At: proposal.CreatedAt}
		if bot, err := h.Store.GetBot(ctx, proposal.BotID); err == nil {
			if thread, err := h.Store.ThreadForBot(ctx, *bot); err == nil {
				item.ConversationID = thread.ID
			}
		}
		items = append(items, item)
	}
	return items, nil
}

func (h *Host) attention(w http.ResponseWriter, r *http.Request) {
	items, err := h.Attention(r.Context())
	writeResult(w, items, err)
}
