package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	sdk "github.com/helpin-ai/agent-runtime-go"
	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

type manageJobInput struct {
	JobID            string               `json:"job_id"`
	Action           string               `json:"action"`
	ExpectedRevision int                  `json:"expected_revision,omitempty"`
	Spec             *sdk.WorkJobSpec     `json:"spec,omitempty"`
	RunID            string               `json:"run_id,omitempty"`
	Command          *sdk.SteerRunRequest `json:"command,omitempty"`
}

func (h *Host) registerJobManagement(server *protocol.Server, botID string) {
	server.AddTool(&protocol.Tool{Name: "manage_batch_job", Description: "Manage an existing job owned by you. Pause/resume controls dispatch; resume cannot bypass worker authentication or approvals. Revise changes the specification and preserves accepted results. Set spec.steering.content and spec.steering.denied_tools to steer all active and future workers; denied_tools are enforced restrictions, never grants. Inspect each worker receipt before claiming application. Steer delivers a durable instruction to one active worker with an acknowledgement; inspect receipts before claiming it applied. Never create a replacement job just to change instructions.", InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"job_id", "action"}, "properties": map[string]any{
		"job_id": map[string]any{"type": "string"}, "action": map[string]any{"type": "string", "enum": []string{"pause", "resume", "revise", "steer", "inspect"}}, "expected_revision": map[string]any{"type": "integer"}, "spec": map[string]any{"type": "object"}, "run_id": map[string]any{"type": "string"}, "command": map[string]any{"type": "object"},
	}}}, func(ctx context.Context, request *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
		var input manageJobInput
		if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
			return toolError(errors.New("invalid management request")), nil
		}
		result, err := h.manageJob(ctx, botID, input)
		if err != nil {
			return toolError(err), nil
		}
		return toolResult(result), nil
	})
}
func (h *Host) manageJob(ctx context.Context, botID string, input manageJobInput) (any, error) {
	record, err := h.botJob(ctx, botID, input.JobID)
	if err != nil {
		return nil, err
	}
	job, err := h.Runtime.GetWorkJob(ctx, record.ID)
	if err != nil {
		return nil, err
	}
	switch input.Action {
	case "pause":
		return h.Runtime.PauseWorkJob(ctx, record.ID)
	case "resume":
		if job.WaitingReason == sdk.WorkJobWaitingWorkerAuth {
			return nil, errors.New("resolve the worker's connection sign-in; Resume does not authenticate workers")
		}
		return h.Runtime.ResumeWorkJob(ctx, record.ID)
	case "revise":
		if input.Spec == nil {
			return nil, errors.New("spec is required")
		}
		return h.Runtime.ReviseWorkJob(ctx, record.ID, sdk.ReviseWorkJobRequest{ExpectedRevision: input.ExpectedRevision, Spec: *input.Spec, Revalidate: false})
	case "steer", "inspect":
		owned := false
		for _, id := range job.ActiveRuns {
			if id == input.RunID {
				owned = true
				break
			}
		}
		if !owned && input.Action == "inspect" {
			attempts, err := h.Runtime.ListWorkAttempts(ctx, record.ID)
			if err != nil {
				return nil, err
			}
			for _, attempt := range attempts {
				if attempt.RunID == input.RunID {
					owned = true
					break
				}
			}
		}
		if !owned {
			return nil, errors.New("run must belong to this job; steering requires an active worker")
		}
		if input.Action == "inspect" {
			return h.Runtime.RunSteering(ctx, input.RunID)
		}
		if input.Command == nil {
			return nil, errors.New("command is required")
		}
		input.Command.ExternalActorID = "bot:" + botID
		return h.Runtime.SteerRun(ctx, input.RunID, *input.Command)
	default:
		return nil, fmt.Errorf("unknown job action")
	}
}
