package rex

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	sdk "github.com/helpin-ai/agent-runtime-go"
)

// RuntimeClient adds Rex's conversation and user-provenance policy to the SDK.
type RuntimeClient struct {
	*sdk.Client
	BaseURL string
}

type Agent = sdk.Agent
type Run = sdk.AgentRun
type Message = sdk.AgentRunMessage
type Artifact = sdk.AgentRunArtifact
type Interaction = sdk.AgentRunInteraction
type ToolCall = sdk.ToolCall
type RunEventEnvelope = sdk.EventEnvelope
type RunModel = sdk.RunModel
type ModelEndpoint = sdk.ModelEndpoint
type ModelCredential = sdk.ModelCredential
type RunMCPServer = sdk.RunMCPServer
type RunMCPTool = sdk.RunMCPTool
type RunMCPCredential = sdk.RunMCPCredential

// StartRunSpec contains Rex's decisions before constructing the SDK request.
type StartRunSpec struct {
	AgentID         string
	HostRunID       string
	TargetType      string
	TargetID        string
	Instructions    string
	Chat            bool
	Model           *RunModel
	ModelCredential *ModelCredential
	Metadata        map[string]any
	MCPServers      []RunMCPServer
	ExternalActorID string
}

// Keep the historical actor ID because it is stored with existing runs.
const LocalActorID = "dost-local-user"

func NewRuntimeClient(baseURL, token string) (*RuntimeClient, error) {
	client, err := sdk.NewClient(baseURL, sdk.WithAppID(AppID), sdk.WithServiceToken(token))
	if err != nil {
		return nil, err
	}
	return &RuntimeClient{Client: client, BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/")}, nil
}

func (c *RuntimeClient) WaitReady(ctx context.Context) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := c.Health(ctx); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for agent runtime: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (c *RuntimeClient) StartRun(ctx context.Context, spec StartRunSpec) (*Run, error) {
	policy := sdk.TurnPolicy{Mode: sdk.TurnPolicyCompleteOnFinish}
	mode := "manual"
	if spec.Chat {
		policy = sdk.TurnPolicy{Mode: sdk.TurnPolicyPauseAfterAssist, IdleTimeoutSeconds: 30 * 24 * 3600}
		mode = "interactive"
	}
	return c.Client.StartRun(ctx, sdk.StartRunRequest{
		AgentID: spec.AgentID, HostRunID: spec.HostRunID,
		Target:       sdk.TargetRef{Type: spec.TargetType, ID: spec.TargetID},
		Instructions: spec.Instructions, Mode: mode, TurnPolicy: policy,
		Model: spec.Model, ModelCredential: spec.ModelCredential,
		Metadata: spec.Metadata, MCPServers: spec.MCPServers, ExternalActorID: spec.ExternalActorID,
	})
}

func (c *RuntimeClient) ResumeRun(ctx context.Context, runID, intent, content, interactionID, resumeID string) (*Run, error) {
	return c.Client.ResumeRun(ctx, runID, sdk.ResumeRunRequest{
		Intent: intent, Content: content, InteractionID: interactionID, ResumeID: resumeID,
		MessageProvenance: "human", ExternalActorID: LocalActorID,
	})
}

func (c *RuntimeClient) RotateMCPCredential(ctx context.Context, runID, serverID string, credential RunMCPCredential) error {
	_, err := c.Client.UpdateRunMCPCredential(ctx, runID, serverID, sdk.UpdateRunMCPCredentialRequest{Credential: credential})
	return err
}

func (c *RuntimeClient) ResumeAuthCompleted(ctx context.Context, runID, interactionID string) (*Run, error) {
	return c.Client.ResumeRun(ctx, runID, sdk.ResumeRunRequest{
		Intent: "auth_completed", InteractionID: interactionID,
		ResumeID: "auth-" + interactionID + "-" + randomHex(4),
	})
}

func (c *RuntimeClient) NotifyRun(ctx context.Context, runID, content, resumeID string) (*Run, error) {
	return c.Client.ResumeRun(ctx, runID, sdk.ResumeRunRequest{
		Intent: "reply", Content: content, ResumeID: resumeID, MessageProvenance: "system_notification",
	})
}

func (c *RuntimeClient) ConversationTranscript(ctx context.Context, runIDs []string) ([]Message, error) {
	var all []Message
	for _, id := range runIDs {
		items, err := c.ListMessages(ctx, id)
		if err != nil {
			return nil, err
		}
		for index := range items {
			if items[index].RunID == "" {
				items[index].RunID = id
			}
		}
		all = append(all, items...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].CreatedAt.Before(all[j].CreatedAt) })
	return all, nil
}
