package rex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// RuntimeClient talks to the bundled agent-runtime over its loopback HTTP API.
type RuntimeClient struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

type Agent struct {
	ID                    string          `json:"id"`
	AppID                 string          `json:"app_id"`
	Name                  string          `json:"name"`
	RuntimeKind           string          `json:"runtime_kind"`
	Provider              string          `json:"provider,omitempty"`
	Model                 string          `json:"model,omitempty"`
	SystemPrompt          string          `json:"system_prompt,omitempty"`
	AllowedTools          []string        `json:"allowed_tools,omitempty"`
	AllowedTargets        []string        `json:"allowed_targets,omitempty"`
	ApprovalMode          string          `json:"approval_mode"`
	DefaultInvocationMode string          `json:"default_invocation_mode"`
	ExecutionConfig       json.RawMessage `json:"execution_config,omitempty"`
	Skills                []SkillRef      `json:"skills"`
}

type Run struct {
	ID            string         `json:"id"`
	AppID         string         `json:"app_id"`
	HostRunID     string         `json:"host_run_id,omitempty"`
	AgentID       string         `json:"agent_id"`
	Status        string         `json:"status"`
	PauseReason   string         `json:"pause_reason,omitempty"`
	ErrorMessage  string         `json:"error_message,omitempty"`
	OutputSummary map[string]any `json:"output_summary,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

func (r Run) Terminal() bool {
	switch r.Status {
	case "completed", "failed", "cancelled":
		return true
	}
	return false
}

type Message struct {
	ID          string    `json:"id"`
	RunID       string    `json:"run_id"`
	Role        string    `json:"role"`
	MessageType string    `json:"message_type,omitempty"`
	Content     string    `json:"content"`
	CreatedAt   time.Time `json:"created_at"`
}

type Interaction struct {
	ID        string          `json:"id"`
	RunID     string          `json:"run_id"`
	Kind      string          `json:"interaction_kind"`
	Status    string          `json:"status"`
	Title     string          `json:"title,omitempty"`
	Summary   string          `json:"summary,omitempty"`
	Payload   json.RawMessage `json:"request_payload,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type ToolCall struct {
	ID        string          `json:"id"`
	RunID     string          `json:"run_id"`
	ToolName  string          `json:"tool_name"`
	Status    string          `json:"status"`
	Input     json.RawMessage `json:"input,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type RunEventEnvelope struct {
	EventID    string         `json:"event_id,omitempty"`
	SentAt     time.Time      `json:"sent_at,omitempty"`
	SequenceNo int64          `json:"sequence_no,omitempty"`
	AppID      string         `json:"app_id"`
	RunID      string         `json:"run_id"`
	HostRunID  string         `json:"host_run_id,omitempty"`
	Type       string         `json:"type"`
	Data       map[string]any `json:"data,omitempty"`
}

// RunModel selects the model for one run. Local endpoints carry their
// approved endpoint binding; hosted providers only need provider and model.
type RunModel struct {
	Provider string         `json:"provider"`
	Model    string         `json:"model"`
	Endpoint *ModelEndpoint `json:"endpoint,omitempty"`
}

type ModelEndpoint struct {
	ID       string `json:"id"`
	BaseURL  string `json:"base_url"`
	AuthMode string `json:"auth_mode"`
}

type ModelCredential struct {
	Type         string     `json:"type"`
	APIKey       string     `json:"api_key,omitempty"`
	AccessToken  string     `json:"access_token,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	ConnectionID string     `json:"connection_id"`
	AccountID    string     `json:"account_id,omitempty"`
}

type RunMCPServer struct {
	ServerID   string            `json:"server_id"`
	ServerName string            `json:"server_name"`
	Transport  string            `json:"transport"`
	URL        string            `json:"url"`
	Tools      []RunMCPTool      `json:"tools"`
	Credential *RunMCPCredential `json:"credential,omitempty"`
}

type RunMCPTool struct {
	Name   string `json:"name"`
	Access string `json:"access"`
}

type RunMCPCredential struct {
	Type        string            `json:"type"`
	AccessToken string            `json:"access_token,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	// ExpiresAt lets the runtime pause for a new token instead of failing.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// StartRunSpec is everything the host decides about a new run.
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

// LocalActorID identifies the Mac's user to the runtime. It marks messages
// as coming from the trusted human, which the TypeSafe reviewer requires.
// LocalActorID keeps its name from when Rex was called Dost; agent-runtime
// stores it with runs and memories.
const LocalActorID = "dost-local-user"

func NewRuntimeClient(baseURL, token string) *RuntimeClient {
	return &RuntimeClient{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, Client: &http.Client{Timeout: 30 * time.Second}}
}

func (c *RuntimeClient) WaitReady(ctx context.Context) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/healthz", nil)
		resp, err := c.Client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for agent runtime: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (c *RuntimeClient) Capabilities(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := c.do(ctx, http.MethodGet, "/v1/capabilities?app_id="+AppID, nil, &out)
	return out, err
}

func (c *RuntimeClient) UpsertAgent(ctx context.Context, agent Agent) (*Agent, error) {
	agent.AppID = AppID
	if agent.ID == "" {
		return nil, errors.New("agent id is required")
	}
	var out Agent
	err := c.do(ctx, http.MethodPut, "/v1/agents/"+url.PathEscape(agent.ID)+"?app_id="+AppID, agent, &out)
	return &out, err
}

func (c *RuntimeClient) GetRun(ctx context.Context, id string) (*Run, error) {
	var out Run
	err := c.do(ctx, http.MethodGet, "/v1/runs/"+url.PathEscape(id)+"?app_id="+AppID, nil, &out)
	return &out, err
}

func (c *RuntimeClient) ListRuns(ctx context.Context) ([]Run, error) {
	var out []Run
	err := c.do(ctx, http.MethodGet, "/v1/runs?app_id="+AppID, nil, &out)
	return out, err
}

func (c *RuntimeClient) ListMessages(ctx context.Context, id string) ([]Message, error) {
	var out []Message
	err := c.do(ctx, http.MethodGet, "/v1/runs/"+url.PathEscape(id)+"/messages?app_id="+AppID, nil, &out)
	return out, err
}

func (c *RuntimeClient) ListInteractions(ctx context.Context, id string) ([]Interaction, error) {
	var out []Interaction
	err := c.do(ctx, http.MethodGet, "/v1/runs/"+url.PathEscape(id)+"/interactions?app_id="+AppID, nil, &out)
	return out, err
}

func (c *RuntimeClient) ListToolCalls(ctx context.Context, id string) ([]ToolCall, error) {
	var out []ToolCall
	err := c.do(ctx, http.MethodGet, "/v1/runs/"+url.PathEscape(id)+"/tool-calls?app_id="+AppID, nil, &out)
	return out, err
}

func (c *RuntimeClient) ListRunEvents(ctx context.Context, id string) ([]RunEventEnvelope, error) {
	var out []RunEventEnvelope
	err := c.do(ctx, http.MethodGet, "/v1/runs/"+url.PathEscape(id)+"/events/history?app_id="+AppID, nil, &out)
	return out, err
}

// OpenRunEvents opens the runtime's SSE stream for one run. The caller owns
// the response body.
func (c *RuntimeClient) OpenRunEvents(ctx context.Context, id string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/runs/"+url.PathEscape(id)+"/events?app_id="+AppID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "text/event-stream")
	client := *c.Client
	client.Timeout = 0
	return client.Do(req)
}

func (c *RuntimeClient) CancelRun(ctx context.Context, id string) (*Run, error) {
	var out Run
	err := c.do(ctx, http.MethodPost, "/v1/runs/"+url.PathEscape(id)+"/cancel?app_id="+AppID, nil, &out)
	return &out, err
}

func (c *RuntimeClient) StartRun(ctx context.Context, spec StartRunSpec) (*Run, error) {
	policy := map[string]any{"mode": "complete_on_finish"}
	mode := "manual"
	if spec.Chat {
		policy = map[string]any{"mode": "pause_after_assistant", "idle_timeout_seconds": 30 * 24 * 3600}
		mode = "interactive"
	}
	payload := map[string]any{
		"app_id": AppID, "agent_id": spec.AgentID,
		"target":       map[string]any{"type": spec.TargetType, "id": spec.TargetID},
		"instructions": spec.Instructions, "mode": mode, "turn_policy": policy,
	}
	if spec.HostRunID != "" {
		payload["host_run_id"] = spec.HostRunID
	}
	if spec.ExternalActorID != "" {
		payload["external_actor_id"] = spec.ExternalActorID
	}
	if spec.Model != nil {
		payload["model"] = spec.Model
	}
	if spec.ModelCredential != nil {
		payload["model_credential"] = spec.ModelCredential
	}
	if len(spec.Metadata) > 0 {
		payload["metadata"] = spec.Metadata
	}
	if len(spec.MCPServers) > 0 {
		payload["mcp_servers"] = spec.MCPServers
	}
	var out Run
	err := c.do(ctx, http.MethodPost, "/v1/runs", payload, &out)
	return &out, err
}

func (c *RuntimeClient) ResumeRun(ctx context.Context, runID, intent, content, interactionID, resumeID string) (*Run, error) {
	// Replies and approvals come from the person at the keyboard.
	payload := map[string]any{"intent": intent, "content": content, "resume_id": resumeID, "message_provenance": "human", "external_actor_id": LocalActorID}
	if interactionID != "" {
		payload["interaction_id"] = interactionID
	}
	var out Run
	err := c.do(ctx, http.MethodPost, "/v1/runs/"+url.PathEscape(runID)+"/resume?app_id="+AppID, payload, &out)
	return &out, err
}

// RotateMCPCredential gives a paused run a new token for one MCP server.
func (c *RuntimeClient) RotateMCPCredential(ctx context.Context, runID, serverID string, credential RunMCPCredential) error {
	path := "/v1/runs/" + url.PathEscape(runID) + "/mcp-servers/" + url.PathEscape(serverID) + "/credential?app_id=" + AppID
	return c.do(ctx, http.MethodPut, path, map[string]any{"credential": credential}, nil)
}

// ResumeAuthCompleted continues a run that paused for MCP sign-in. It is a
// host event: the runtime does not treat it as the user's words.
func (c *RuntimeClient) ResumeAuthCompleted(ctx context.Context, runID, interactionID string) (*Run, error) {
	payload := map[string]any{"intent": "auth_completed", "interaction_id": interactionID, "resume_id": "auth-" + interactionID + "-" + randomHex(4)}
	var out Run
	err := c.do(ctx, http.MethodPost, "/v1/runs/"+url.PathEscape(runID)+"/resume?app_id="+AppID, payload, &out)
	return &out, err
}

// NotifyRun delivers a message Rex writes itself, such as a task from the
// main bot or a report back to it, to a run waiting for its next message.
// It is marked as a system notification, never as the user's own words.
func (c *RuntimeClient) NotifyRun(ctx context.Context, runID, content, resumeID string) (*Run, error) {
	payload := map[string]any{"intent": "reply", "content": content, "resume_id": resumeID, "message_provenance": "system_notification"}
	var out Run
	err := c.do(ctx, http.MethodPost, "/v1/runs/"+url.PathEscape(runID)+"/resume?app_id="+AppID, payload, &out)
	return &out, err
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

// RuntimeError carries the HTTP status of a failed runtime call.
type RuntimeError struct {
	Status  int
	Message string
}

func (e *RuntimeError) Error() string {
	return fmt.Sprintf("agent runtime returned %d: %s", e.Status, e.Message)
}

func (c *RuntimeClient) do(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var decoded map[string]any
		_ = json.Unmarshal(payload, &decoded)
		message := strings.TrimSpace(string(payload))
		if value, ok := decoded["error"].(string); ok {
			message = value
		}
		return &RuntimeError{Status: resp.StatusCode, Message: message}
	}
	if output != nil && len(payload) > 0 {
		return json.Unmarshal(payload, output)
	}
	return nil
}
