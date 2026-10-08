package rex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPConnection is a remote MCP server the user connected. Tools are
// discovered from the server; the user marks each as read or write, and
// write tools follow the bot's approval policy.
type MCPConnection struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	Auth       string `json:"auth"`
	Token      string `json:"token,omitempty"`
	ClearToken bool   `json:"clear_token,omitempty"`
	HasToken   bool   `json:"has_token"`
	// SignedIn is true for an OAuth connection with a working sign-in.
	SignedIn  bool      `json:"signed_in"`
	Tools     []MCPTool `json:"tools"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	oauth     oauthMeta
}

type MCPTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Access      string `json:"access"`
	Enabled     bool   `json:"enabled"`
}

var serverNamePattern = regexp.MustCompile(`^[A-Za-z0-9 _.-]{1,40}$`)

func mcpTokenKey(id string) string { return "mcp." + id + ".token" }

func (h *Host) ListMCPConnections(ctx context.Context) ([]MCPConnection, error) {
	items, err := h.Store.ListMCPConnections(ctx)
	if err != nil {
		return nil, err
	}
	for index := range items {
		token, _ := h.Settings.secrets.Get(mcpTokenKey(items[index].ID))
		items[index].HasToken = token != ""
		items[index].SignedIn = items[index].oauth.SignedIn
	}
	return items, nil
}

// SaveMCPConnection validates the connection, stores its token in the
// keychain and refreshes its tool list from the server.
func (h *Host) SaveMCPConnection(ctx context.Context, input MCPConnection) (*MCPConnection, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.URL = strings.TrimSpace(input.URL)
	if !serverNamePattern.MatchString(input.Name) {
		return nil, errors.New("name must be 1–40 letters, numbers, spaces, dots, dashes or underscores")
	}
	parsed, err := url.Parse(input.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("URL must be an http(s) URL without credentials, query or fragment")
	}
	if input.Auth != "bearer" && input.Auth != "oauth" {
		input.Auth = "none"
	}
	existing := map[string]MCPTool{}
	if input.ID == "" {
		input.ID = uuid.NewString()
	} else if previous, err := h.Store.GetMCPConnection(ctx, input.ID); err == nil {
		input.CreatedAt = previous.CreatedAt
		if input.Auth == "oauth" {
			input.oauth = previous.oauth
		}
		for _, tool := range previous.Tools {
			existing[tool.Name] = tool
		}
	}
	for _, tool := range input.Tools {
		existing[tool.Name] = tool
	}
	switch {
	case input.ClearToken || input.Auth != "bearer":
		_ = h.Settings.secrets.Delete(mcpTokenKey(input.ID))
	case strings.TrimSpace(input.Token) != "":
		if err := h.Settings.secrets.Set(mcpTokenKey(input.ID), strings.TrimSpace(input.Token)); err != nil {
			return nil, fmt.Errorf("store token in keychain: %w", err)
		}
	}
	if input.Auth != "oauth" {
		_ = h.Settings.secrets.Delete(mcpOAuthKey(input.ID))
		input.oauth = oauthMeta{}
	}
	input.Token, input.ClearToken = "", false
	tools, err := h.discoverMCPTools(ctx, input)
	if input.Auth == "oauth" && !input.oauth.SignedIn {
		input.Status = "Sign in to connect"
	} else if err != nil {
		input.Status = "Could not reach the server: " + err.Error()
		input.Tools = nil
		for _, tool := range existing {
			input.Tools = append(input.Tools, tool)
		}
	} else {
		input.Status = fmt.Sprintf("Connected · %d tools", len(tools))
		for index, tool := range tools {
			if previous, ok := existing[tool.Name]; ok {
				tools[index].Access, tools[index].Enabled = previous.Access, previous.Enabled
			}
		}
		input.Tools = tools
	}
	if err := h.Store.SaveMCPConnection(ctx, &input); err != nil {
		return nil, err
	}
	token, _ := h.Settings.secrets.Get(mcpTokenKey(input.ID))
	input.HasToken = token != ""
	input.SignedIn = input.oauth.SignedIn
	return &input, nil
}

func (h *Host) DeleteMCPConnection(ctx context.Context, id string) error {
	_ = h.Settings.secrets.Delete(mcpTokenKey(id))
	_ = h.Settings.secrets.Delete(mcpOAuthKey(id))
	return h.Store.DeleteMCPConnection(ctx, id)
}

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

func (h *Host) discoverMCPTools(ctx context.Context, connection MCPConnection) ([]MCPTool, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	if connection.Auth == "oauth" && !connection.oauth.SignedIn {
		return nil, errors.New("sign in first")
	}
	token, _, err := h.accessToken(ctx, connection, false)
	if err != nil {
		return nil, err
	}
	if token != "" {
		client.Transport = bearerTransport{token: token, base: http.DefaultTransport}
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	session, err := protocol.NewClient(&protocol.Implementation{Name: "rex", Version: "1.0.0"}, nil).
		Connect(ctx, &protocol.StreamableClientTransport{Endpoint: connection.URL, HTTPClient: client, MaxRetries: -1}, nil)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	var tools []MCPTool
	var cursor string
	for page := 0; page < 10; page++ {
		result, err := session.ListTools(ctx, &protocol.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, tool := range result.Tools {
			access := "write"
			if tool.Annotations != nil && tool.Annotations.ReadOnlyHint {
				access = "read"
			}
			tools = append(tools, MCPTool{Name: tool.Name, Description: excerpt(tool.Description, 300), Access: access, Enabled: true})
		}
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	return tools, nil
}

// MCPServersForBot builds the run-scoped attachments for a bot's selected
// connections. Credentials are read from the keychain for each run.
func (h *Host) MCPServersForBot(ctx context.Context, bot Bot) ([]RunMCPServer, error) {
	var servers []RunMCPServer
	for _, id := range bot.MCPConnections {
		connection, err := h.Store.GetMCPConnection(ctx, id)
		if err != nil {
			continue
		}
		var tools []RunMCPTool
		for _, tool := range connection.Tools {
			if tool.Enabled {
				tools = append(tools, RunMCPTool{Name: tool.Name, Access: tool.Access})
			}
		}
		if len(tools) == 0 {
			continue
		}
		server := RunMCPServer{ServerID: connection.ID, ServerName: connection.Name, Transport: "streamable_http", URL: connection.URL, Tools: tools}
		token, expires, err := h.accessToken(ctx, *connection, false)
		if err != nil {
			if errors.Is(err, ErrSignInRequired) {
				return nil, fmt.Errorf("sign in to %s again in Apps to use it", connection.Name)
			}
			return nil, err
		}
		if token != "" {
			server.Credential = &RunMCPCredential{Type: "bearer_token", AccessToken: token, ExpiresAt: expires}
		}
		servers = append(servers, server)
	}
	return servers, nil
}

// --- store ---

func scanMCPConnection(row interface{ Scan(...any) error }) (*MCPConnection, error) {
	var item MCPConnection
	var tools string
	var oauth string
	if err := row.Scan(&item.ID, &item.Name, &item.URL, &item.Auth, &tools, &item.Status, &item.CreatedAt, &item.UpdatedAt, &oauth); err != nil {
		return nil, err
	}
	if oauth != "" {
		_ = json.Unmarshal([]byte(oauth), &item.oauth)
	}
	_ = json.Unmarshal([]byte(tools), &item.Tools)
	if item.Tools == nil {
		item.Tools = []MCPTool{}
	}
	return &item, nil
}

func (s *Store) ListMCPConnections(ctx context.Context) ([]MCPConnection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,url,auth,tools,status,created_at,updated_at,oauth FROM mcp_connections ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []MCPConnection{}
	for rows.Next() {
		item, err := scanMCPConnection(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (s *Store) GetMCPConnection(ctx context.Context, id string) (*MCPConnection, error) {
	item, err := scanMCPConnection(s.db.QueryRowContext(ctx, `SELECT id,name,url,auth,tools,status,created_at,updated_at,oauth FROM mcp_connections WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return item, err
}

func (s *Store) SaveMCPConnection(ctx context.Context, item *MCPConnection) error {
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	if item.Tools == nil {
		item.Tools = []MCPTool{}
	}
	tools, _ := json.Marshal(item.Tools)
	oauth, _ := json.Marshal(item.oauth)
	_, err := s.db.ExecContext(ctx, `INSERT INTO mcp_connections(id,name,url,auth,tools,status,created_at,updated_at,oauth) VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name,url=excluded.url,auth=excluded.auth,tools=excluded.tools,status=excluded.status,updated_at=excluded.updated_at,oauth=excluded.oauth`,
		item.ID, item.Name, item.URL, item.Auth, string(tools), item.Status, item.CreatedAt, item.UpdatedAt, string(oauth))
	return err
}

func (s *Store) DeleteMCPConnection(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM mcp_connections WHERE id=?`, id)
	return err
}
