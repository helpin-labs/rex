package rex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/helpin-ai/agent-runtime-go/mcpauth"
)

// fakeOAuthServer is an MCP resource with its own authorization server.
func fakeOAuthServer(t *testing.T) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/.well-known/oauth-protected-resource"):
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": server.URL + "/mcp", "authorization_servers": []string{server.URL}})
		case r.URL.Path == "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": server.URL, "authorization_endpoint": server.URL + "/authorize",
				"token_endpoint": server.URL + "/token", "registration_endpoint": server.URL + "/register", "code_challenge_methods_supported": []string{"S256"}})
		case r.URL.Path == "/register":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "client-1", "token_endpoint_auth_method": "none"})
		case r.URL.Path == "/token":
			_ = r.ParseForm()
			switch {
			case r.Form.Get("grant_type") == "authorization_code" && r.Form.Get("code") == "good-code" && r.Form.Get("code_verifier") != "":
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-1", "refresh_token": "refresh-1", "token_type": "Bearer", "expires_in": 60})
			case r.Form.Get("grant_type") == "refresh_token" && r.Form.Get("refresh_token") == "refresh-1":
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-2", "token_type": "Bearer", "expires_in": 3600})
			default:
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
			}
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestMCPSignInStoresTokensInTheKeychainAndRefreshes(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	server := fakeOAuthServer(t)

	connection, err := host.SaveMCPConnection(ctx, MCPConnection{Name: "Notes", URL: server.URL + "/mcp", Auth: "oauth"})
	if err != nil {
		t.Fatal(err)
	}
	if connection.SignedIn || connection.Status != "Sign in to connect" {
		t.Fatalf("a new OAuth connection waits for sign-in: %+v", connection)
	}
	if _, _, err := host.accessToken(ctx, *connection, false); !errors.Is(err, ErrSignInRequired) {
		t.Fatalf("no token before sign-in, got %v", err)
	}

	signInURL, err := host.StartOAuth(ctx, connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(signInURL)
	query := parsed.Query()
	if parsed.Path != "/authorize" || query.Get("client_id") != "client-1" || query.Get("code_challenge_method") != "S256" || query.Get("redirect_uri") != host.oauthRedirectURI() {
		t.Fatalf("sign-in uses PKCE and Rex's callback: %s", signInURL)
	}
	if _, err := host.CompleteOAuth(ctx, "forged-state", "good-code", ""); err == nil {
		t.Fatal("an unknown state is refused")
	}
	if _, err := host.CompleteOAuth(ctx, query.Get("state"), "good-code", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := host.CompleteOAuth(ctx, query.Get("state"), "good-code", ""); err == nil {
		t.Fatal("a sign-in state works once")
	}

	stored, _ := host.Store.GetMCPConnection(ctx, connection.ID)
	raw, _ := json.Marshal(stored)
	if strings.Contains(string(raw), "access-1") || strings.Contains(string(raw), "refresh-1") {
		t.Fatalf("tokens never leave the Keychain: %s", raw)
	}
	if secrets, _ := host.oauthSecretsFor(connection.ID); secrets.RefreshToken != "refresh-1" {
		t.Fatalf("the refresh token is in the Keychain: %+v", secrets)
	}

	// The first token expires within the refresh window, so it is refreshed.
	token, expires, err := host.accessToken(ctx, *stored, false)
	if err != nil || token != "access-2" || expires == nil {
		t.Fatalf("an expiring token is refreshed: %q %v %v", token, expires, err)
	}
	if token, _, _ := host.accessToken(ctx, *stored, false); token != "access-2" {
		t.Fatalf("a fresh token is reused, got %q", token)
	}

	// A rejected refresh asks the user to sign in again.
	secrets, _ := host.oauthSecretsFor(connection.ID)
	secrets.RefreshToken = "revoked"
	_ = host.saveOAuthSecrets(connection.ID, secrets)
	if _, _, err := host.accessToken(ctx, *stored, true); !errors.Is(err, ErrSignInRequired) {
		t.Fatalf("a revoked sign-in needs the user, got %v", err)
	}
	if again, _ := host.Store.GetMCPConnection(ctx, connection.ID); again.oauth.SignedIn {
		t.Fatal("the connection is marked for sign-in")
	}
}

func TestSignInAsksForTheServersScopesAndARefreshToken(t *testing.T) {
	configuration := &mcpauth.Configuration{
		Resource:      mcpauth.ProtectedResourceMetadata{ScopesSupported: []string{"user:read", "account:read"}},
		Authorization: mcpauth.AuthorizationServerMetadata{ScopesSupported: []string{"offline_access", "user:read", "zones:write"}},
	}
	if got := strings.Join(requestedScopes(configuration), " "); got != "user:read account:read offline_access" {
		t.Fatalf("scopes = %q", got)
	}
}
