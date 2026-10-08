package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/helpin-ai/agent-runtime-go/mcpauth"
	"golang.org/x/net/publicsuffix"
)

// OAuth for MCP connections, following agent-runtime-go/mcpauth: Rex
// discovers the server's authorization server, registers itself as a
// client, sends the user's browser to sign in (PKCE) and receives the code
// on its own loopback address. The refresh token, access token and client
// secret live in the Keychain. Runs only get a short-lived access token
// with its expiry; when one expires mid-run, Rex refreshes it and resumes.

// ErrSignInRequired means a connection's sign-in expired or was revoked.
var ErrSignInRequired = errors.New("sign in to this connection again")

const (
	oauthStateTTL     = 10 * time.Minute
	oauthRefreshSkew  = 2 * time.Minute
	oauthCallbackPath = "/oauth/mcp/callback"
)

// oauthMeta is a connection's non-secret OAuth configuration, stored with
// the connection.
type oauthMeta struct {
	TokenEndpoint string     `json:"token_endpoint,omitempty"`
	Resource      string     `json:"resource,omitempty"`
	ClientID      string     `json:"client_id,omitempty"`
	AuthMethod    string     `json:"auth_method,omitempty"`
	RedirectURI   string     `json:"redirect_uri,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	// SignedIn is false until the first sign-in and after a refresh is
	// rejected.
	SignedIn bool `json:"signed_in"`
}

// oauthSecrets are kept in the Keychain as one JSON item per connection.
type oauthSecrets struct {
	ClientSecret string `json:"client_secret,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

func mcpOAuthKey(id string) string { return "mcp." + id + ".oauth" }

type pendingSignIn struct {
	connectionID string
	verifier     string
	expires      time.Time
}

// mcpOAuth holds sign-ins in progress (in memory: they only need to outlive
// one browser round trip) and one lock per connection for refreshes.
type mcpOAuth struct {
	mu      sync.Mutex
	pending map[string]pendingSignIn
	locks   map[string]*sync.Mutex
}

func (o *mcpOAuth) lock(id string) *sync.Mutex {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.locks == nil {
		o.locks = map[string]*sync.Mutex{}
	}
	if o.locks[id] == nil {
		o.locks[id] = &sync.Mutex{}
	}
	return o.locks[id]
}

func (h *Host) oauthRedirectURI() string { return h.cfg.HostURL + oauthCallbackPath }

// oauthClient allows the server's own site for the authorization server
// (mcp.example.com may sign in at auth.example.com). Local mode lets plain
// http through only for addresses on this Mac: Rex, as a desktop app,
// receives the sign-in on its loopback callback (RFC 8252), and servers
// running on this Mac use http. Every other URL still has to be https on
// the server's own host or domain.
func oauthClient(connection MCPConnection) (*mcpauth.Client, error) {
	parsed, err := url.Parse(connection.URL)
	if err != nil {
		return nil, err
	}
	options := []mcpauth.Option{mcpauth.WithInsecureLocalhost()}
	if domain, err := publicsuffix.EffectiveTLDPlusOne(parsed.Hostname()); err == nil {
		options = append(options, mcpauth.WithAllowedHosts(domain, "*."+domain))
	}
	return mcpauth.NewClient(connection.URL, options...)
}

// requestedScopes asks for what the server says its tools need, plus
// offline_access when offered, so Rex gets a refresh token and the user
// does not have to sign in again whenever the access token expires.
func requestedScopes(configuration *mcpauth.Configuration) []string {
	scopes := append([]string(nil), configuration.Resource.ScopesSupported...)
	for _, scope := range configuration.Authorization.ScopesSupported {
		if scope == "offline_access" && !slices.Contains(scopes, scope) {
			scopes = append(scopes, scope)
		}
	}
	return scopes
}

// oauthError keeps the SDK's reason code, which its message leaves out.
func oauthError(action string, err error) error {
	var remote *mcpauth.RemoteError
	if errors.As(err, &remote) && remote.Code != "" {
		return fmt.Errorf("%s: %v (%s)", action, err, strings.ReplaceAll(remote.Code, "_", " "))
	}
	return fmt.Errorf("%s: %w", action, err)
}

func (h *Host) oauthSecretsFor(id string) (oauthSecrets, error) {
	var secrets oauthSecrets
	raw, err := h.Settings.secrets.Get(mcpOAuthKey(id))
	if err != nil || raw == "" {
		return secrets, err
	}
	err = json.Unmarshal([]byte(raw), &secrets)
	return secrets, err
}

func (h *Host) saveOAuthSecrets(id string, secrets oauthSecrets) error {
	payload, err := json.Marshal(secrets)
	if err != nil {
		return err
	}
	return h.Settings.secrets.Set(mcpOAuthKey(id), string(payload))
}

// StartOAuth prepares a sign-in and returns the URL to open in the browser.
func (h *Host) StartOAuth(ctx context.Context, connectionID string) (string, error) {
	connection, err := h.Store.GetMCPConnection(ctx, connectionID)
	if err != nil {
		return "", err
	}
	if connection.Auth != "oauth" {
		return "", errors.New("this connection does not use sign-in")
	}
	client, err := oauthClient(*connection)
	if err != nil {
		return "", err
	}
	configuration, err := client.Discover(ctx)
	if err != nil {
		return "", oauthError("this server does not offer sign-in Rex can use", err)
	}
	meta := connection.oauth
	secrets, err := h.oauthSecretsFor(connection.ID)
	if err != nil {
		return "", err
	}
	meta.TokenEndpoint = configuration.Authorization.TokenEndpoint
	meta.Resource = firstNonEmpty(strings.TrimSpace(configuration.Resource.Resource), connection.URL)
	// Register again when Rex's callback address changed.
	if meta.ClientID == "" || meta.RedirectURI != h.oauthRedirectURI() {
		registration, err := client.Register(ctx, configuration.Authorization.RegistrationEndpoint, h.oauthRedirectURI())
		if err != nil {
			return "", oauthError("could not register Rex with this server", err)
		}
		meta.ClientID, meta.RedirectURI = registration.ClientID, h.oauthRedirectURI()
		meta.AuthMethod = firstNonEmpty(registration.TokenEndpointAuthMethod, "none")
		secrets.ClientSecret = registration.ClientSecret
		if err := h.saveOAuthSecrets(connection.ID, secrets); err != nil {
			return "", fmt.Errorf("store sign-in in keychain: %w", err)
		}
	}
	request, err := client.NewAuthorizationRequest(configuration, meta.ClientID, meta.RedirectURI, requestedScopes(configuration))
	if err != nil {
		return "", err
	}
	connection.oauth = meta
	if err := h.Store.SaveMCPConnection(ctx, connection); err != nil {
		return "", err
	}
	h.oauth.mu.Lock()
	if h.oauth.pending == nil {
		h.oauth.pending = map[string]pendingSignIn{}
	}
	now := time.Now()
	for key, item := range h.oauth.pending {
		if now.After(item.expires) {
			delete(h.oauth.pending, key)
		}
	}
	h.oauth.pending[mcpauth.HashState(request.State)] = pendingSignIn{connectionID: connection.ID, verifier: request.Verifier, expires: now.Add(oauthStateTTL)}
	h.oauth.mu.Unlock()
	return request.URL, nil
}

// CompleteOAuth finishes a sign-in from the browser callback. Each state
// works once and only for the connection that started it.
func (h *Host) CompleteOAuth(ctx context.Context, state, code, providerError string) (*MCPConnection, error) {
	h.oauth.mu.Lock()
	pending, ok := h.oauth.pending[mcpauth.HashState(state)]
	delete(h.oauth.pending, mcpauth.HashState(state))
	h.oauth.mu.Unlock()
	if !ok || time.Now().After(pending.expires) {
		return nil, errors.New("this sign-in link expired; start again from Rex")
	}
	if providerError != "" {
		return nil, errors.New("sign-in was cancelled or refused")
	}
	if strings.TrimSpace(code) == "" {
		return nil, errors.New("the server did not send a sign-in code")
	}
	connection, err := h.Store.GetMCPConnection(ctx, pending.connectionID)
	if err != nil {
		return nil, err
	}
	client, err := oauthClient(*connection)
	if err != nil {
		return nil, err
	}
	secrets, err := h.oauthSecretsFor(connection.ID)
	if err != nil {
		return nil, err
	}
	meta := connection.oauth
	token, err := client.ExchangeCode(ctx, meta.TokenEndpoint, meta.ClientID, secrets.ClientSecret, meta.AuthMethod, code, pending.verifier, meta.RedirectURI, meta.Resource)
	if err != nil {
		return nil, oauthError("sign-in could not be completed", err)
	}
	if err := h.storeToken(ctx, connection, token, secrets); err != nil {
		return nil, err
	}
	// Read the tools now that Rex may call them, then continue runs that
	// were waiting for this sign-in.
	saved, err := h.SaveMCPConnection(ctx, *connection)
	if err != nil {
		return nil, err
	}
	go h.resumeRunsWaitingFor(context.Background(), connection.ID)
	return saved, nil
}

func (h *Host) storeToken(ctx context.Context, connection *MCPConnection, token *mcpauth.Token, secrets oauthSecrets) error {
	secrets.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		secrets.RefreshToken = token.RefreshToken
	}
	if err := h.saveOAuthSecrets(connection.ID, secrets); err != nil {
		return fmt.Errorf("store sign-in in keychain: %w", err)
	}
	connection.oauth.ExpiresAt = token.ExpiresAt
	connection.oauth.SignedIn = true
	return h.Store.SaveMCPConnection(ctx, connection)
}

// accessToken returns the credential a run or tool discovery uses for a
// connection. Signed-in connections are refreshed shortly before expiry,
// or always when force is set (the server rejected the token).
func (h *Host) accessToken(ctx context.Context, connection MCPConnection, force bool) (string, *time.Time, error) {
	switch connection.Auth {
	case "bearer":
		token, err := h.Settings.secrets.Get(mcpTokenKey(connection.ID))
		if err != nil || token == "" {
			return "", nil, fmt.Errorf("the %s connection has no token; add it in Apps", connection.Name)
		}
		return token, nil, nil
	case "oauth":
	default:
		return "", nil, nil
	}
	lock := h.oauth.lock(connection.ID)
	lock.Lock()
	defer lock.Unlock()
	current, err := h.Store.GetMCPConnection(ctx, connection.ID)
	if err != nil {
		return "", nil, err
	}
	secrets, err := h.oauthSecretsFor(current.ID)
	if err != nil {
		return "", nil, err
	}
	meta := current.oauth
	if !meta.SignedIn || secrets.AccessToken == "" {
		return "", nil, fmt.Errorf("%w: %s", ErrSignInRequired, current.Name)
	}
	fresh := meta.ExpiresAt == nil || time.Until(*meta.ExpiresAt) > oauthRefreshSkew
	if fresh && !force {
		return secrets.AccessToken, meta.ExpiresAt, nil
	}
	if secrets.RefreshToken == "" {
		h.requireSignIn(ctx, current)
		return "", nil, fmt.Errorf("%w: %s", ErrSignInRequired, current.Name)
	}
	client, err := oauthClient(*current)
	if err != nil {
		return "", nil, err
	}
	token, err := client.Refresh(ctx, meta.TokenEndpoint, meta.ClientID, secrets.ClientSecret, meta.AuthMethod, secrets.RefreshToken, meta.Resource)
	if err != nil {
		if mcpauth.IsStatus(err, http.StatusBadRequest) || mcpauth.IsStatus(err, http.StatusUnauthorized) {
			h.requireSignIn(ctx, current)
			return "", nil, fmt.Errorf("%w: %s", ErrSignInRequired, current.Name)
		}
		return "", nil, oauthError("could not refresh the "+current.Name+" sign-in", err)
	}
	if err := h.storeToken(ctx, current, token, secrets); err != nil {
		return "", nil, err
	}
	return token.AccessToken, token.ExpiresAt, nil
}

func (h *Host) requireSignIn(ctx context.Context, connection *MCPConnection) {
	connection.oauth.SignedIn = false
	connection.Status = "Sign in again to use this connection"
	if err := h.Store.SaveMCPConnection(ctx, connection); err != nil {
		slog.Warn("could not mark connection for sign-in", "connection", connection.ID, "error", err)
	}
}

// SignOut forgets a connection's tokens.
func (h *Host) SignOut(ctx context.Context, connectionID string) error {
	connection, err := h.Store.GetMCPConnection(ctx, connectionID)
	if err != nil {
		return err
	}
	if err := h.Settings.secrets.Delete(mcpOAuthKey(connectionID)); err != nil {
		return err
	}
	connection.oauth = oauthMeta{}
	connection.Status = "Signed out"
	return h.Store.SaveMCPConnection(ctx, connection)
}

// resumeAuthPaused continues a run that paused because an MCP server
// rejected or outlived its token: Rex refreshes the token, hands the run
// the new one and resumes it. It reports whether the run was resumed.
func (h *Host) resumeAuthPaused(ctx context.Context, run *Run) bool {
	interactions, err := h.Runtime.ListInteractions(ctx, run.ID)
	if err != nil {
		return false
	}
	for _, interaction := range interactions {
		if interaction.Status != "pending" || interaction.Kind != "authentication" {
			continue
		}
		var request struct {
			Provider string `json:"provider"`
			ServerID string `json:"server_id"`
		}
		_ = json.Unmarshal(interaction.Payload, &request)
		if request.Provider != "mcp" || request.ServerID == "" {
			return false
		}
		connection, err := h.Store.GetMCPConnection(ctx, request.ServerID)
		if err != nil {
			return false
		}
		token, expires, err := h.accessToken(ctx, *connection, true)
		if err != nil {
			slog.Info("run waits for the user to sign in", "run", run.ID, "connection", connection.ID, "error", err)
			return false
		}
		if err := h.Runtime.RotateMCPCredential(ctx, run.ID, connection.ID, RunMCPCredential{Type: "bearer_token", AccessToken: token, ExpiresAt: expires}); err != nil {
			slog.Warn("could not hand the run a new token", "run", run.ID, "error", err)
			return false
		}
		if _, err := h.Runtime.ResumeAuthCompleted(ctx, run.ID, interaction.ID); err != nil {
			slog.Warn("could not resume the run after sign-in", "run", run.ID, "error", err)
			return false
		}
		h.Watch(run.ID)
		return true
	}
	return false
}

// resumeRunsWaitingFor continues every conversation run that paused for a
// connection the user just signed in to.
func (h *Host) resumeRunsWaitingFor(ctx context.Context, connectionID string) {
	runIDs, err := h.Store.ActiveRunIDs(ctx)
	if err != nil {
		return
	}
	for _, id := range runIDs {
		run, err := h.Runtime.GetRun(ctx, id)
		if err != nil || run.Status != "paused" || run.PauseReason != "authentication" {
			continue
		}
		h.resumeAuthPaused(ctx, run)
	}
}

// --- HTTP ---

func (h *Host) startOAuth(w http.ResponseWriter, r *http.Request) {
	signInURL, err := h.StartOAuth(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": signInURL})
}

func (h *Host) signOut(w http.ResponseWriter, r *http.Request) {
	if err := h.SignOut(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// oauthCallback is where the provider sends the browser back. It needs no
// UI token: the single-use state proves the sign-in started in Rex.
func (h *Host) oauthCallback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	connection, err := h.CompleteOAuth(r.Context(), query.Get("state"), query.Get("code"), query.Get("error"))
	title, message := "Connected", "You can close this tab and return to Rex."
	if err != nil {
		title, message = "Sign-in failed", err.Error()
	} else {
		title = "Connected to " + connection.Name
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	_, _ = fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Rex</title><style>body{font:15px -apple-system,sans-serif;display:grid;place-content:center;height:90vh;text-align:center;color:#1f2125}h1{font-size:20px}</style><h1>%s</h1><p>%s</p>`,
		html.EscapeString(title), html.EscapeString(message))
}
