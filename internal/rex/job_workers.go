package rex

import (
	"context"
	"errors"
	"net/http"
	"strings"

	sdk "github.com/helpin-ai/agent-runtime-go"
)

// ErrNotJobWorker means the run is not an open worker of the job.
var ErrNotJobWorker = errors.New("this worker has finished or belongs to another job")

// WorkerMessage is the user's message to one worker of a job. The command ID
// makes a retried send idempotent.
type WorkerMessage struct {
	CommandID string `json:"command_id"`
	Content   string `json:"content"`
}

// MessageJobWorker delivers the user's message to one worker. A working
// worker is steered: it reads the message at its next safe boundary. A
// worker paused for the user gets it as the user's reply, which answers its
// question and lets it continue.
func (h *Host) MessageJobWorker(ctx context.Context, jobID, runID string, message WorkerMessage) error {
	message.Content = strings.TrimSpace(message.Content)
	if message.Content == "" || len(message.Content) > 32768 || message.CommandID == "" || len(message.CommandID) > 100 {
		return errors.New("a message is required")
	}
	delivered := runID + "/" + message.CommandID
	if _, done := h.workerMessages.Load(delivered); done {
		return nil
	}
	if err := h.openJobWorker(ctx, jobID, runID); err != nil {
		return err
	}
	if err := h.deliverWorkerMessage(ctx, runID, message); err != nil {
		return err
	}
	h.workerMessages.Store(delivered, struct{}{})
	return nil
}

func (h *Host) deliverWorkerMessage(ctx context.Context, runID string, message WorkerMessage) error {
	run, err := h.Runtime.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	h.touch()
	switch run.Status {
	case "queued", "running":
		return h.steerWorker(ctx, runID, message, LocalActorID)
	case "paused":
		interactionID := ""
		if interactions, err := h.Runtime.ListInteractions(ctx, runID); err == nil {
			for _, interaction := range interactions {
				if interaction.Status != "pending" {
					continue
				}
				switch interaction.InteractionKind {
				case "request_user_input":
					interactionID = interaction.ID
				case "authentication":
					return errors.New("this worker is waiting for a connection sign-in; sign in again, then it continues")
				default:
					return errors.New("this worker is waiting for your approval; allow or decline it first")
				}
			}
		}
		if run.PauseReason == sdk.PauseReasonAuth {
			return errors.New("this worker is waiting for a connection sign-in; sign in again, then it continues")
		}
		_, err := h.Runtime.ResumeRun(ctx, runID, "reply", message.Content, interactionID, "job-reply-"+message.CommandID)
		if err == nil {
			h.wakeJobs()
		}
		return err
	}
	return ErrNotJobWorker
}

// steerWorker sends a steering instruction from actor at the worker's latest
// revision, retrying once when another instruction landed in between.
func (h *Host) steerWorker(ctx context.Context, runID string, message WorkerMessage, actor string) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		var commands []sdk.RunSteeringCommand
		if commands, err = h.Runtime.RunSteering(ctx, runID); err != nil {
			return err
		}
		revision := int64(0)
		for _, command := range commands {
			if command.CommandID == message.CommandID {
				return nil // already delivered
			}
			revision = max(revision, command.Revision)
		}
		_, err = h.Runtime.SteerRun(ctx, runID, sdk.SteerRunRequest{CommandID: message.CommandID, ExpectedRevision: revision, Content: message.Content, ExternalActorID: actor})
		var upstream *sdk.HTTPStatusError
		if err == nil || !errors.As(err, &upstream) || upstream.StatusCode != http.StatusConflict {
			return err
		}
	}
	return err
}

// openJobWorker checks that runID is a worker of jobID that has not ended.
func (h *Host) openJobWorker(ctx context.Context, jobID, runID string) error {
	attempts, err := h.Runtime.ListWorkAttempts(ctx, jobID)
	if err != nil {
		return err
	}
	for _, attempt := range attempts {
		if attempt.RunID == runID && attempt.EndedAt == nil {
			return nil
		}
	}
	return ErrNotJobWorker
}

func (h *Host) messageJobWorker(w http.ResponseWriter, r *http.Request) {
	record, err := h.Store.GetJob(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("job not found"))
		return
	}
	var input WorkerMessage
	if !decode(w, r, &input) {
		return
	}
	if err := h.MessageJobWorker(r.Context(), record.ID, r.PathValue("run_id"), input); err != nil {
		status := steeringStatus(err)
		if errors.Is(err, ErrNotJobWorker) {
			status = http.StatusConflict
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, h.jobView(r.Context(), *record, true))
}
