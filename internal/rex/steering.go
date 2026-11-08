package rex

import (
	"context"
	"errors"
	"net/http"
	"strings"

	sdk "github.com/helpin-ai/agent-runtime-go"
)

// ErrRunChanged means the conversation moved to another run since the user
// looked; an instruction meant for the old run must not reach its successor.
var ErrRunChanged = errors.New("the conversation moved to a new run; reopen it before sending instructions")

// SteerRequest is the user's instruction for a working run. The command ID
// makes a retried send idempotent; the expected revision is the latest one
// the user saw, so two instructions never race.
type SteerRequest struct {
	CommandID        string `json:"command_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Content          string `json:"content"`
}

// steeringRun checks that runID is still the conversation's active run, the
// run the user is looking at.
func (h *Host) steeringRun(ctx context.Context, conversationID, runID string) error {
	conversation, err := h.Store.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if conversation.ActiveRunID == nil || *conversation.ActiveRunID != runID {
		return ErrRunChanged
	}
	return nil
}

// SteerConversationRun sends the user's instruction to the conversation's
// working run. The runtime adds it to the conversation at the next safe
// boundary, between model turns, so the bot changes course without stopping.
func (h *Host) SteerConversationRun(ctx context.Context, conversationID, runID string, req SteerRequest) (*sdk.RunSteeringCommand, error) {
	req.Content = strings.TrimSpace(req.Content)
	if req.Content == "" || len(req.Content) > 32768 || req.CommandID == "" || len(req.CommandID) > 128 || req.ExpectedRevision < 0 {
		return nil, errors.New("invalid instruction")
	}
	if err := h.steeringRun(ctx, conversationID, runID); err != nil {
		return nil, err
	}
	h.touch()
	return h.Runtime.SteerRun(ctx, runID, sdk.SteerRunRequest{
		CommandID: req.CommandID, ExpectedRevision: req.ExpectedRevision, Content: req.Content, ExternalActorID: LocalActorID,
	})
}

// ConversationRunSteering lists the instructions sent to the conversation's
// active run, with whether each was applied.
func (h *Host) ConversationRunSteering(ctx context.Context, conversationID, runID string) ([]sdk.RunSteeringCommand, error) {
	if err := h.steeringRun(ctx, conversationID, runID); err != nil {
		return nil, err
	}
	return h.Runtime.RunSteering(ctx, runID)
}

func (h *Host) runSteering(w http.ResponseWriter, r *http.Request) {
	conversationID, runID := r.PathValue("id"), r.PathValue("run_id")
	if r.Method == http.MethodGet {
		commands, err := h.ConversationRunSteering(r.Context(), conversationID, runID)
		if err != nil {
			writeError(w, steeringStatus(err), err)
			return
		}
		if commands == nil {
			commands = []sdk.RunSteeringCommand{}
		}
		writeJSON(w, http.StatusOK, commands)
		return
	}
	var input SteerRequest
	if !decode(w, r, &input) {
		return
	}
	command, err := h.SteerConversationRun(r.Context(), conversationID, runID, input)
	if err != nil {
		status := steeringStatus(err)
		if status == http.StatusBadGateway {
			err = errors.New("the instruction was not accepted; check that the bot is still working and try again")
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusAccepted, command)
}

func steeringStatus(err error) int {
	var upstream *sdk.HTTPStatusError
	switch {
	case errors.Is(err, ErrRunChanged):
		return http.StatusConflict
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.As(err, &upstream) && upstream.StatusCode >= 400 && upstream.StatusCode < 500:
		return upstream.StatusCode
	case errors.As(err, &upstream):
		return http.StatusBadGateway
	}
	return http.StatusBadRequest
}
