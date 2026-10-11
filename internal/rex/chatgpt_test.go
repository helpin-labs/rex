package rex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/helpin-ai/agent-runtime-go/chatgptauth"
)

// fakeChatGPT is ChatGPT's device-code sign-in: the code is approved on the
// second poll, and every token exchange issues a new access token.
func fakeChatGPT(t *testing.T, expiresIn int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var issued, polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			fmt.Fprint(w, `{"device_auth_id":"dev-1","user_code":"ABCD-1234","interval":"1"}`)
		case "/api/accounts/deviceauth/token":
			if polls.Add(1) < 2 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			fmt.Fprint(w, `{"authorization_code":"code","code_verifier":"verifier"}`)
		case "/oauth/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") == "refresh_token" && r.Form.Get("refresh_token") != "refresh" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			n := issued.Add(1)
			claims, _ := json.Marshal(map[string]any{"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "acct-1"}, "n": n})
			jwt := "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
			fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"refresh","expires_in":%d}`, jwt, expiresIn)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, &issued
}

func testChatGPTAuth(t *testing.T, server *httptest.Server) *ChatGPTAuth {
	t.Helper()
	auth := NewChatGPTAuth(NewMemorySecrets())
	client, err := chatgptauth.NewClient(chatgptauth.Config{Issuer: server.URL, AllowLocalHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	auth.client = client
	return auth
}

func TestChatGPTSignInAndRunCredential(t *testing.T) {
	ctx := context.Background()
	server, issued := fakeChatGPT(t, 3600)
	auth := testChatGPTAuth(t, server)

	status, err := auth.StartLogin(ctx)
	if err != nil || status.Pending == nil || status.Pending.UserCode != "ABCD-1234" || !strings.HasSuffix(status.Pending.VerificationURL, "/codex/device") || status.Connected {
		t.Fatalf("start: %+v %v", status, err)
	}
	// The first poll is still pending; the code then gets approved.
	for i := 0; i < 5 && !status.Connected; i++ {
		auth.device.NextPollAt = time.Time{}
		if status, err = auth.PollLogin(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if !status.Connected || status.Pending != nil {
		t.Fatalf("not signed in: %+v", status)
	}

	credential, err := auth.Credential(ctx, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if credential.Type != "oauth" || credential.AccountID != "acct-1" || credential.ConnectionID != chatGPTConnectionID || credential.ExpiresAt == nil || credential.AccessToken == "" {
		t.Fatalf("credential=%+v", credential)
	}
	if issued.Load() != 1 {
		t.Fatalf("a fresh token was refreshed anyway: %d issued", issued.Load())
	}

	// A forced refresh for a token that is no longer current reuses the
	// newer one; for the current token it refreshes.
	again, _ := auth.Credential(ctx, true, "not-the-current-token")
	if again.AccessToken != credential.AccessToken || issued.Load() != 1 {
		t.Fatal("refreshed for a stale fingerprint")
	}
	fresh, err := auth.Credential(ctx, true, tokenFingerprint(credential.AccessToken))
	if err != nil || fresh.AccessToken == credential.AccessToken || issued.Load() != 2 {
		t.Fatalf("forced refresh: %v, issued %d", err, issued.Load())
	}

	if err := auth.SignOut(); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Credential(ctx, false, ""); err == nil {
		t.Fatal("signed out but still has a credential")
	}
}

func TestChatGPTTokenNearExpiryIsRefreshed(t *testing.T) {
	ctx := context.Background()
	server, issued := fakeChatGPT(t, 30) // expires within the minute
	auth := testChatGPTAuth(t, server)
	signIn(t, auth)
	if _, err := auth.Credential(ctx, false, ""); err != nil || issued.Load() != 2 {
		t.Fatalf("near-expiry token not refreshed: %v, issued %d", err, issued.Load())
	}
}

func TestChatGPTBotRunsOnTheSubscription(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	server, _ := fakeChatGPT(t, 3600)
	client, _ := chatgptauth.NewClient(chatgptauth.Config{Issuer: server.URL, AllowLocalHTTP: true})
	host.Settings.ChatGPT.client = client

	bot := Bot{ID: "writer", Name: "Writer", Provider: ProviderChatGPT}
	if _, _, err := host.Settings.RunModelFor(ctx, bot); err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("ran without a sign-in: %v", err)
	}
	signIn(t, host.Settings.ChatGPT)
	model, credential, err := host.Settings.RunModelFor(ctx, bot)
	if err != nil || model.Provider != "openai_chatgpt" || model.Model != ChatGPTModels[0] || credential == nil || credential.Type != "oauth" {
		t.Fatalf("model=%+v credential=%+v err=%v", model, credential, err)
	}
	settings, _ := host.Settings.Get(ctx)
	if !settings.ChatGPT.Connected {
		t.Fatal("settings do not show the sign-in")
	}

	// The runtime's refresh callback gets the credential back for the same
	// account, and is refused for anything else.
	call := func(body map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		recorder := httptest.NewRecorder()
		host.refreshChatGPTCredential(recorder, httptest.NewRequest(http.MethodPost, "/runtime/model-credentials/refresh", strings.NewReader(string(raw))))
		return recorder
	}
	ok := call(map[string]any{"app_id": AppID, "provider": "openai_chatgpt", "connection_id": chatGPTConnectionID, "account_id": "acct-1", "reason": "expired", "credential_fingerprint": tokenFingerprint(credential.AccessToken)})
	var reply struct {
		Credential ModelCredential `json:"credential"`
	}
	if ok.Code != http.StatusOK || json.Unmarshal(ok.Body.Bytes(), &reply) != nil || reply.Credential.AccountID != "acct-1" || reply.Credential.Type != "oauth" {
		t.Fatalf("refresh callback: %d %s", ok.Code, ok.Body)
	}
	if bad := call(map[string]any{"app_id": AppID, "provider": "openai_chatgpt", "connection_id": chatGPTConnectionID, "account_id": "someone-else"}); bad.Code != http.StatusUnauthorized {
		t.Fatalf("another account got a credential: %d", bad.Code)
	}
}

func TestKernelKeyAvailableForPerBotCloudSelection(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	settings, _ := host.Settings.Get(ctx)
	settings.Kernel.APIKey = "sk_kernel"
	settings, _, err := host.Settings.Save(ctx, settings)
	if err != nil {
		t.Fatal(err)
	}
	hasKernel := func() bool {
		env, err := host.Settings.RuntimeEnv(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Contains(strings.Join(env, "\n"), "KERNEL_API_KEY=sk_kernel")
	}
	if settings.CloudBrowsers() || !hasKernel() {
		t.Fatal("a stored key must support explicit per-bot cloud without changing the default")
	}
	settings.KernelBrowser = true
	saved, restart, err := host.Settings.Save(ctx, settings)
	if err != nil || !restart || !saved.CloudBrowsers() || !hasKernel() {
		t.Fatalf("switch on: restart=%v cloud=%v err=%v", restart, saved.CloudBrowsers(), err)
	}
	if _, err := host.Browser.TakeOver(ctx, "any"); err == nil {
		t.Fatal("takeover allowed with cloud browsers")
	}
}

// signIn completes the fake device-code sign-in.
func signIn(t *testing.T, auth *ChatGPTAuth) {
	t.Helper()
	ctx := context.Background()
	if _, err := auth.StartLogin(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		auth.device.NextPollAt = time.Time{}
		status, err := auth.PollLogin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if status.Connected {
			return
		}
	}
	t.Fatal("sign-in never completed")
}
