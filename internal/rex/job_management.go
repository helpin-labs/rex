package rex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/helpin-ai/agent-runtime-go"
	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

type manageJobInput struct {
	JobID            string           `json:"job_id"`
	Action           string           `json:"action"`
	ExpectedRevision int              `json:"expected_revision,omitempty"`
	Spec             *sdk.WorkJobSpec `json:"spec,omitempty"`
	RunID            string           `json:"run_id,omitempty"`
	// Message is the instruction for one worker (action steer).
	Message string `json:"message,omitempty"`
	// Command is the older form of a steer; only its content is used.
	Command *sdk.SteerRunRequest `json:"command,omitempty"`
	// requestKey identifies the tool call, so a retried call is one command.
	requestKey string
}

func (h *Host) registerJobManagement(server *protocol.Server, botID string) {
	server.AddTool(&protocol.Tool{Name: "manage_batch_job", Description: "Manage an existing job owned by you. Pause/resume controls dispatch; resume cannot bypass worker authentication or approvals. Revise changes the specification and preserves accepted results. Set spec.steering.content and spec.steering.denied_tools to steer all active and future workers; denied_tools are enforced restrictions, never grants. Inspect each worker receipt before claiming application. Steer sends one active worker an instruction: pass run_id and message (plain words); Rex delivers it at the worker's next safe boundary and returns that worker's receipts, so check its state before claiming it applied. Never create a replacement job just to change instructions.", InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"job_id", "action"}, "properties": map[string]any{
		"job_id": map[string]any{"type": "string"}, "action": map[string]any{"type": "string", "enum": []string{"pause", "resume", "revise", "steer", "inspect"}}, "expected_revision": map[string]any{"type": "integer"}, "spec": map[string]any{"type": "object"}, "run_id": map[string]any{"type": "string", "description": "The worker run for steer and inspect."}, "message": map[string]any{"type": "string", "maxLength": 32768, "description": "For steer: the instruction for that worker."},
	}}}, func(ctx context.Context, request *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
		var input manageJobInput
		if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
			return toolError(errors.New("invalid management request")), nil
		}
		input.requestKey = jobRequestKey(request.Params.Meta)
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
		content := strings.TrimSpace(input.Message)
		if content == "" && input.Command != nil {
			content = strings.TrimSpace(input.Command.Content)
		}
		if content == "" {
			return nil, errors.New("message is required: the instruction for this worker")
		}
		// Rex owns the command ID and revision: the ID comes from the tool
		// call, so the runtime retrying the call delivers it once.
		commandID := "bot-" + randomHex(8)
		if input.requestKey != "" {
			sum := sha256.Sum256([]byte(input.requestKey + "\x00" + input.RunID))
			commandID = "bot-" + hex.EncodeToString(sum[:16])
		}
		if err := h.steerWorker(ctx, input.RunID, WorkerMessage{CommandID: commandID, Content: content}, "bot:"+botID); err != nil {
			return nil, err
		}
		return h.Runtime.RunSteering(ctx, input.RunID)
	default:
		return nil, fmt.Errorf("unknown job action")
	}
}
