package rex

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/helpin-ai/agent-runtime-go/mcpauth"
	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Google apps (Gmail, Google Sheets) are connections Rex serves itself.
// Google offers no MCP servers Rex can register with, so Rex signs in to
// Google as a desktop app (PKCE, on its loopback callback) and provides
// the tools from its own MCP endpoint. A connection's URL names the
// service ("google:gmail"); runs reach it at /mcp/google/<connection id>
// with a per-connection token, and Rex adds the Google access token,
// refreshed as needed, when it calls Google. Google tokens never reach a run.

// Google's endpoints. Tests point them at a fake server.
var (
	googleAuthURL   = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL  = "https://oauth2.googleapis.com/token"
	googleRevokeURL = "https://oauth2.googleapis.com/revoke"
	gmailAPI        = "https://gmail.googleapis.com/gmail/v1/users/me"
	sheetsAPI       = "https://sheets.googleapis.com/v4/spreadsheets"
	driveAPI        = "https://www.googleapis.com/drive/v3"
)

// The Google client Rex signs in as. Release builds set these with
// -ldflags "-X github.com/helpin-ai/rex/internal/rex.googleClientID=…";
// REX_GOOGLE_CLIENT_ID and REX_GOOGLE_CLIENT_SECRET override them, and a
// client the user adds in Apps overrides both. A desktop client's secret
// is not confidential (Google says so); PKCE protects the sign-in.
var googleClientID, googleClientSecret string

const googleClientKey = "google.client"

type GoogleClient struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
}

// GoogleStatus tells the UI whether Rex can sign in to Google, and where
// the client comes from: "settings" (added by the user), "built-in" or "".
type GoogleStatus struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source"`
	ClientID   string `json:"client_id,omitempty"`
}

func (h *Host) googleClient() (GoogleClient, string) {
	if raw, err := h.Settings.secrets.Get(googleClientKey); err == nil && raw != "" {
		var client GoogleClient
		if json.Unmarshal([]byte(raw), &client) == nil && client.ClientID != "" {
			return client, "settings"
		}
	}
	if id := strings.TrimSpace(os.Getenv("REX_GOOGLE_CLIENT_ID")); id != "" {
		return GoogleClient{ClientID: id, ClientSecret: strings.TrimSpace(os.Getenv("REX_GOOGLE_CLIENT_SECRET"))}, "built-in"
	}
	if googleClientID != "" {
		return GoogleClient{ClientID: googleClientID, ClientSecret: googleClientSecret}, "built-in"
	}
	return GoogleClient{}, ""
}

func (h *Host) GoogleStatus() GoogleStatus {
	client, source := h.googleClient()
	return GoogleStatus{Configured: client.ClientID != "", Source: source, ClientID: client.ClientID}
}

// SetGoogleClient stores the user's own Google client, or forgets it when
// the ID is empty. Sign-ins belong to the client that made them, so every
// Google app has to sign in again afterwards.
func (h *Host) SetGoogleClient(ctx context.Context, client GoogleClient) (GoogleStatus, error) {
	client.ClientID, client.ClientSecret = strings.TrimSpace(client.ClientID), strings.TrimSpace(client.ClientSecret)
	previous, _ := h.googleClient()
	if client.ClientID == "" {
		if err := h.Settings.secrets.Delete(googleClientKey); err != nil {
			return GoogleStatus{}, err
		}
	} else {
		if !strings.HasSuffix(client.ClientID, ".apps.googleusercontent.com") {
			return GoogleStatus{}, errors.New("a Google client ID ends in .apps.googleusercontent.com")
		}
		payload, _ := json.Marshal(client)
		if err := h.Settings.secrets.Set(googleClientKey, string(payload)); err != nil {
			return GoogleStatus{}, fmt.Errorf("store the Google client in keychain: %w", err)
		}
	}
	if current, _ := h.googleClient(); current != previous {
		connections, err := h.Store.ListMCPConnections(ctx)
		if err != nil {
			return GoogleStatus{}, err
		}
		for _, connection := range connections {
			if connection.Auth == "google" && connection.oauth.SignedIn {
				if err := h.SignOut(ctx, connection.ID); err != nil {
					return GoogleStatus{}, err
				}
			}
		}
	}
	return h.GoogleStatus(), nil
}

// --- sign-in ---

// startGoogleSignIn returns the Google consent URL for a connection.
func (h *Host) startGoogleSignIn(ctx context.Context, connection *MCPConnection) (string, error) {
	service := googleServices[connection.URL]
	if service == nil {
		return "", fmt.Errorf("unknown Google app %q", connection.URL)
	}
	client, _ := h.googleClient()
	if client.ClientID == "" {
		return "", errors.New("add a Google client in Apps before signing in to Google")
	}
	secrets, err := h.oauthSecretsFor(connection.ID)
	if err != nil {
		return "", err
	}
	secrets.ClientSecret = client.ClientSecret
	if err := h.saveOAuthSecrets(connection.ID, secrets); err != nil {
		return "", fmt.Errorf("store sign-in in keychain: %w", err)
	}
	connection.oauth.TokenEndpoint = googleTokenURL
	connection.oauth.ClientID = client.ClientID
	connection.oauth.RedirectURI = h.oauthRedirectURI()
	connection.oauth.AuthMethod = "client_secret_post"
	if err := h.Store.SaveMCPConnection(ctx, connection); err != nil {
		return "", err
	}
	state, verifier := randomURLSafe(32), randomURLSafe(48)
	challenge := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"client_id":             {client.ClientID},
		"redirect_uri":          {h.oauthRedirectURI()},
		"response_type":         {"code"},
		"scope":                 {strings.Join(service.requestScopes(), " ")},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
		// A refresh token, every time: Google only sends one with consent.
		"access_type": {"offline"},
		"prompt":      {"consent"},
	}
	h.rememberSignIn(state, connection.ID, verifier)
	return googleAuthURL + "?" + query.Encode(), nil
}

func (h *Host) rememberSignIn(state, connectionID, verifier string) {
	h.oauth.mu.Lock()
	defer h.oauth.mu.Unlock()
	if h.oauth.pending == nil {
		h.oauth.pending = map[string]pendingSignIn{}
	}
	now := time.Now()
	for key, item := range h.oauth.pending {
		if now.After(item.expires) {
			delete(h.oauth.pending, key)
		}
	}
	h.oauth.pending[mcpauth.HashState(state)] = pendingSignIn{connectionID: connectionID, verifier: verifier, expires: now.Add(oauthStateTTL)}
}

// randomURLSafe is a random string for PKCE verifiers and sign-in states.
func randomURLSafe(bytes int) string {
	value := make([]byte, bytes)
	_, _ = rand.Read(value)
	return base64.RawURLEncoding.EncodeToString(value)
}

type googleTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	IDToken      string `json:"id_token"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
}

// googleTokenError is a refusal from Google's token endpoint.
type googleTokenError struct {
	Status int
	Code   string
	Detail string
}

func (e *googleTokenError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("Google said %s: %s", strings.ReplaceAll(e.Code, "_", " "), e.Detail)
	}
	return "Google said " + strings.ReplaceAll(firstNonEmpty(e.Code, http.StatusText(e.Status)), "_", " ")
}

func googleToken(ctx context.Context, form url.Values) (*googleTokenResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var token googleTokenResponse
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	_ = json.Unmarshal(body, &token)
	if response.StatusCode != http.StatusOK || token.AccessToken == "" {
		return nil, &googleTokenError{Status: response.StatusCode, Code: token.Error, Detail: token.Description}
	}
	return &token, nil
}

func (t *googleTokenResponse) mcpauthToken() *mcpauth.Token {
	token := &mcpauth.Token{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, ExpiresIn: t.ExpiresIn, Scope: t.Scope}
	if t.ExpiresIn > 0 {
		expires := time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
		token.ExpiresAt = &expires
	}
	return token
}

// completeGoogleSignIn exchanges the code and checks that the user allowed
// everything the app needs: Google lets them untick permissions.
func (h *Host) completeGoogleSignIn(ctx context.Context, connection *MCPConnection, code, verifier string) error {
	service := googleServices[connection.URL]
	if service == nil {
		return fmt.Errorf("unknown Google app %q", connection.URL)
	}
	secrets, err := h.oauthSecretsFor(connection.ID)
	if err != nil {
		return err
	}
	meta := connection.oauth
	token, err := googleToken(ctx, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier},
		"client_id": {meta.ClientID}, "client_secret": {secrets.ClientSecret}, "redirect_uri": {meta.RedirectURI},
	})
	if err != nil {
		return fmt.Errorf("sign-in could not be completed: %w", err)
	}
	if missing := service.missingScopes(token.Scope); len(missing) > 0 {
		return fmt.Errorf("%s needs every permission it asked for. Sign in again and leave all the boxes ticked (missing: %s)", service.name, strings.Join(missing, ", "))
	}
	connection.oauth.Account = idTokenEmail(token.IDToken)
	return h.storeToken(ctx, connection, token.mcpauthToken(), secrets)
}

func (h *Host) refreshGoogleToken(ctx context.Context, connection *MCPConnection, secrets oauthSecrets) (*mcpauth.Token, error) {
	token, err := googleToken(ctx, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {secrets.RefreshToken},
		"client_id": {connection.oauth.ClientID}, "client_secret": {secrets.ClientSecret},
	})
	if err != nil {
		var refused *googleTokenError
		if errors.As(err, &refused) && (refused.Status == http.StatusBadRequest || refused.Status == http.StatusUnauthorized) {
			return nil, ErrSignInRequired
		}
		return nil, fmt.Errorf("could not refresh the %s sign-in: %w", connection.Name, err)
	}
	return token.mcpauthToken(), nil
}

// revokeGoogle tells Google to forget a sign-in, so signing out also
// removes Rex from the user's Google account. It is best effort.
func revokeGoogle(token string) {
	if token == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, googleRevokeURL, strings.NewReader(url.Values{"token": {token}}.Encode()))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if response, err := http.DefaultClient.Do(request); err == nil {
		response.Body.Close()
	}
}

// idTokenEmail reads the account's address from the ID token. It came
// straight from Google's token endpoint over TLS, so it needs no signature
// check (OpenID Connect Core 3.1.3.7).
func idTokenEmail(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	_ = json.Unmarshal(payload, &claims)
	return claims.Email
}

// --- the tool server ---

// googleService is one Google app: the permissions it asks for and its tools.
type googleService struct {
	name        string
	permissions []string
	tools       []googleTool
}

// requestScopes adds the account's address, shown in Apps, to the app's permissions.
func (s *googleService) requestScopes() []string {
	return append([]string{"openid", "email"}, s.permissions...)
}

// missingScopes lists the app's permissions that a grant leaves out.
func (s *googleService) missingScopes(granted string) []string {
	have := strings.Fields(granted)
	var missing []string
	for _, scope := range s.permissions {
		if !slices.Contains(have, scope) {
			missing = append(missing, strings.TrimPrefix(scope, "https://www.googleapis.com/auth/"))
		}
	}
	return missing
}

type googleTool struct {
	name        string
	description string
	// read tools run on their own; write tools follow the bot's approvals.
	access string
	schema map[string]any
	call   func(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error)
}

// googleServices is keyed by connection URL.
var googleServices = map[string]*googleService{
	"google:gmail":  gmailService,
	"google:sheets": sheetsService,
}

func googleToolList(service *googleService) []MCPTool {
	tools := make([]MCPTool, 0, len(service.tools))
	for _, tool := range service.tools {
		tools = append(tools, MCPTool{Name: tool.name, Description: tool.description, Access: tool.access, Enabled: true})
	}
	return tools
}

func (h *Host) GoogleToken(connectionID string) string {
	mac := hmac.New(sha256.New, h.cfg.MCPSecret)
	mac.Write([]byte("google-app:" + connectionID))
	return hex.EncodeToString(mac.Sum(nil))
}

func (h *Host) googleRunServer(connection MCPConnection, tools []RunMCPTool) RunMCPServer {
	return RunMCPServer{
		ServerID: connection.ID, ServerName: connection.Name, Transport: "streamable_http",
		URL: h.cfg.HostURL + "/mcp/google/" + connection.ID, Tools: tools,
		Credential: &RunMCPCredential{Type: "bearer_token", AccessToken: h.GoogleToken(connection.ID)},
	}
}

// googleMCP serves a Google connection's enabled tools.
func (h *Host) googleMCP() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/mcp/google/")
		if id == "" || !bearerMatches(r, h.GoogleToken(id)) {
			writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
			return
		}
		connection, err := h.Store.GetMCPConnection(r.Context(), id)
		if err != nil || connection.Auth != "google" || googleServices[connection.URL] == nil {
			writeError(w, http.StatusNotFound, errors.New("no such Google app"))
			return
		}
		service := googleServices[connection.URL]
		enabled := map[string]bool{}
		for _, tool := range connection.Tools {
			enabled[tool.Name] = tool.Enabled
		}
		server := protocol.NewServer(&protocol.Implementation{Name: "rex-google", Version: "1.0.0"}, nil)
		for _, tool := range service.tools {
			if !enabled[tool.name] {
				continue
			}
			server.AddTool(&protocol.Tool{
				Name: tool.name, Description: tool.description, InputSchema: tool.schema,
				Annotations: &protocol.ToolAnnotations{ReadOnlyHint: tool.access == "read"},
			}, func(ctx context.Context, request *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
				result, err := h.callGoogle(ctx, *connection, tool, request.Params.Arguments)
				if err != nil {
					slog.Info("google tool failed", "app", connection.Name, "tool", tool.name, "error", err)
					payload, _ := json.Marshal(map[string]string{"error": err.Error()})
					return &protocol.CallToolResult{IsError: true, Content: []protocol.Content{&protocol.TextContent{Text: string(payload)}}}, nil
				}
				return toolResult(result), nil
			})
		}
		protocol.NewStreamableHTTPHandler(func(*http.Request) *protocol.Server { return server }, &protocol.StreamableHTTPOptions{Stateless: true, JSONResponse: true}).ServeHTTP(w, r)
	})
}

// callGoogle runs a tool with a current access token. If Google rejects
// the token anyway (revoked early, clock skew), it refreshes once and retries.
func (h *Host) callGoogle(ctx context.Context, connection MCPConnection, tool googleTool, input json.RawMessage) (any, error) {
	if len(input) == 0 || string(input) == "null" {
		input = json.RawMessage("{}")
	}
	for attempt := 0; ; attempt++ {
		token, _, err := h.accessToken(ctx, connection, attempt > 0)
		if err != nil {
			if errors.Is(err, ErrSignInRequired) {
				return nil, fmt.Errorf("the %s sign-in has expired; ask the user to sign in again in Apps", connection.Name)
			}
			return nil, err
		}
		result, err := tool.call(ctx, &googleAPI{token: token, client: &http.Client{Timeout: 45 * time.Second}}, input)
		var failure *googleAPIError
		if attempt == 0 && errors.As(err, &failure) && failure.Status == http.StatusUnauthorized {
			continue
		}
		return result, err
	}
}

// googleAPI calls Google's REST APIs with one access token.
type googleAPI struct {
	token  string
	client *http.Client
}

type googleAPIError struct {
	Status  int
	Message string
}

func (e *googleAPIError) Error() string {
	return fmt.Sprintf("Google returned %d: %s", e.Status, e.Message)
}

// do sends body as JSON (when not nil) and decodes the reply into out.
func (g *googleAPI) do(ctx context.Context, method, endpoint string, body, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(payload))
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+g.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := g.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return err
	}
	if response.StatusCode >= 300 {
		var failure struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &failure)
		return &googleAPIError{Status: response.StatusCode, Message: firstNonEmpty(failure.Error.Message, http.StatusText(response.StatusCode))}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// decodeInput reads a tool's arguments, refusing unknown fields so a
// misspelt argument is reported instead of silently ignored.
func decodeInput(input json.RawMessage, into any) error {
	decoder := json.NewDecoder(strings.NewReader(string(input)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

// --- HTTP ---

func (h *Host) getGoogle(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.GoogleStatus())
}

func (h *Host) setGoogleClient(w http.ResponseWriter, r *http.Request) {
	var input GoogleClient
	if !decode(w, r, &input) {
		return
	}
	status, err := h.SetGoogleClient(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
