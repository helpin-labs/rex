package rex

import (
	"context"
	sdk "github.com/helpin-ai/agent-runtime-go"
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
	// RunID and JobID identify a batch job worker that is waiting.
	RunID string `json:"run_id,omitempty"`
	JobID string `json:"job_id,omitempty"`
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
				switch interaction.InteractionKind {
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
		items = append(items, h.jobAttention(ctx)...)
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

// jobAttention lists what batch job workers are waiting on. Workers are
// runs of their own, outside any conversation, so their questions and
// approvals point at the conversation that started the job.
func (h *Host) jobAttention(ctx context.Context) []AttentionItem {
	var items []AttentionItem
	records, err := h.Store.ListJobs(ctx, "", attentionScope)
	if err != nil {
		return nil
	}
	for _, record := range records {
		if record.NotifiedState == sdk.WorkJobStateCompleted || record.NotifiedState == sdk.WorkJobStateCancelled || record.NotifiedState == sdk.WorkJobStateFailed {
			continue
		}
		job, err := h.Runtime.GetWorkJob(ctx, record.ID)
		if err != nil || job.State == sdk.WorkJobStateCompleted || job.State == sdk.WorkJobStateCancelled || job.State == sdk.WorkJobStateFailed {
			continue
		}
		pending := h.jobPending(ctx, job)
		asked := map[string]bool{}
		for _, item := range pending {
			asked[item.RunID] = true
		}
		// A worker that stopped to talk to the user without a formal
		// question still needs them.
		for _, runID := range job.ActiveRuns {
			if asked[runID] {
				continue
			}
			run, err := h.Runtime.GetRun(ctx, runID)
			if err != nil || run.Status != "paused" || (run.PauseReason != sdk.PauseReasonUserMessage && run.PauseReason != sdk.PauseReasonNone) {
				continue
			}
			items = append(items, AttentionItem{ID: "worker-" + runID, Kind: "question", ConversationID: record.ConversationID, BotID: record.BotID,
				Title: "Batch job \u201c" + record.Title + "\u201d: a worker stopped and is waiting for you", Detail: h.lastAssistantText(ctx, runID),
				At: run.UpdatedAt, RunID: runID, JobID: record.ID})
		}
		for _, pending := range pending {
			kind := "approval"
			switch pending.Interaction.InteractionKind {
			case "request_user_input":
				kind = "question"
			case "authentication":
				kind = "reconnect"
			}
			items = append(items, AttentionItem{ID: pending.Interaction.ID, Kind: kind, ConversationID: record.ConversationID, BotID: record.BotID,
				Title: "Batch job \u201c" + record.Title + "\u201d: " + pending.Interaction.Title, Detail: pending.Interaction.Summary,
				At: pending.Interaction.CreatedAt, RunID: pending.RunID, JobID: record.ID})
		}
	}
	return items
}
