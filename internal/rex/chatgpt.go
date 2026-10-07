package rex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/helpin-ai/agent-runtime-go/chatgptauth"
)

// ChatGPT lets bots run on the user's ChatGPT subscription instead of an
// API key. Signing in uses ChatGPT's device code: Rex shows a short code,
// the user enters it at auth.openai.com, and Rex keeps the resulting token
// in the Keychain. Each run gets the current access token; when it is about
// to expire, the runtime asks Rex for a fresh one (refreshChatGPTCredential).

// ProviderChatGPT is a ChatGPT subscription; the runtime calls it
// openai_chatgpt.
const ProviderChatGPT = "chatgpt"

// chatGPTConnectionID names Rex's single ChatGPT sign-in to the runtime.
const chatGPTConnectionID = "chatgpt"

const chatGPTTokenKey = "provider.chatgpt.oauth"

// ChatGPTModels are the models a ChatGPT subscription offers to apps.
var ChatGPTModels = []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6-sol", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"}

// ChatGPTStatus is what the app shows about the sign-in.
type ChatGPTStatus struct {
	Connected bool       `json:"connected"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// Pending is an unfinished sign-in: the code to enter and where.
	Pending *ChatGPTLogin `json:"pending,omitempty"`
}

type ChatGPTLogin struct {
	VerificationURL string    `json:"verification_url"`
	UserCode        string    `json:"user_code"`
	ExpiresAt       time.Time `json:"expires_at"`
	IntervalSeconds int       `json:"interval_seconds"`
}

// ChatGPTAuth owns the sign-in. One login can be pending at a time; it lives
// in memory because a code is only good for 15 minutes.
type ChatGPTAuth struct {
	secrets Secrets
	client  *chatgptauth.Client
	mu      sync.Mutex
	device  *chatgptauth.DeviceSession
}

func NewChatGPTAuth(secrets Secrets) *ChatGPTAuth {
	client, _ := chatgptauth.NewClient(chatgptauth.Config{})
	return &ChatGPTAuth{secrets: secrets, client: client}
}

func (a *ChatGPTAuth) token() (*chatgptauth.Token, error) {
	raw, err := a.secrets.Get(chatGPTTokenKey)
	if err != nil || raw == "" {
		return nil, err
	}
	var token chatgptauth.Token
	if err := json.Unmarshal([]byte(raw), &token); err != nil {
		return nil, errors.New("the saved ChatGPT sign-in is unreadable; sign in again")
	}
	return &token, nil
}

func (a *ChatGPTAuth) saveToken(token *chatgptauth.Token) error {
	raw, err := json.Marshal(token)
	if err != nil {
		return err
	}
	return a.secrets.Set(chatGPTTokenKey, string(raw))
}

// Status reports whether Rex is signed in to ChatGPT.
func (a *ChatGPTAuth) Status() (ChatGPTStatus, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.statusLocked()
}

func (a *ChatGPTAuth) statusLocked() (ChatGPTStatus, error) {
	var status ChatGPTStatus
	token, err := a.token()
	if err != nil {
		return status, err
	}
	if token != nil {
		status.Connected = true
		expires := token.ExpiresAt
		status.ExpiresAt = &expires
	}
	if a.device != nil && a.device.ExpiresAt.After(time.Now()) {
		status.Pending = &ChatGPTLogin{VerificationURL: a.device.VerificationURL, UserCode: a.device.UserCode, ExpiresAt: a.device.ExpiresAt, IntervalSeconds: a.device.IntervalSeconds}
	}
	return status, nil
}

// StartLogin asks ChatGPT for a device code.
func (a *ChatGPTAuth) StartLogin(ctx context.Context) (ChatGPTStatus, error) {
	session, err := a.client.StartDeviceLogin(ctx)
	if err != nil {
		return ChatGPTStatus{}, chatGPTError(err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.device = session
	return a.statusLocked()
}

// PollLogin checks once whether the user has entered the code, and saves
// the token when they have.
func (a *ChatGPTAuth) PollLogin(ctx context.Context) (ChatGPTStatus, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.device == nil {
		return a.statusLocked()
	}
	result, err := a.client.PollDeviceLogin(ctx, a.device)
	if err != nil {
		a.device = nil
		return ChatGPTStatus{}, chatGPTError(err)
	}
	if result.Token != nil {
		if err := a.saveToken(result.Token); err != nil {
			return ChatGPTStatus{}, err
		}
		a.device = nil
	}
	return a.statusLocked()
}

// SignOut forgets the token and any pending sign-in.
func (a *ChatGPTAuth) SignOut() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.device = nil
	return a.secrets.Delete(chatGPTTokenKey)
}

// Credential returns a run credential, refreshing the token when it expires
// within a minute. force refreshes now, but only if the token the runtime
// rejected (fingerprint) is still the current one; otherwise another run
// already refreshed it.
func (a *ChatGPTAuth) Credential(ctx context.Context, force bool, fingerprint string) (*ModelCredential, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	token, err := a.token()
	if err != nil {
		return nil, err
	}
	if token == nil {
		return nil, errors.New("Rex is not signed in to ChatGPT. Sign in under Settings → Models")
	}
	stale := time.Until(token.ExpiresAt) < time.Minute
	if force && fingerprint != "" && fingerprint != tokenFingerprint(token.AccessToken) {
		force = false
	}
	if stale || force {
		next, err := a.client.Refresh(ctx, *token)
		if err != nil {
			return nil, chatGPTError(err)
		}
		if err := a.saveToken(next); err != nil {
			return nil, err
		}
		token = next
	}
	expires := token.ExpiresAt
	return &ModelCredential{Type: "oauth", AccessToken: token.AccessToken, ExpiresAt: &expires, ConnectionID: chatGPTConnectionID, AccountID: token.AccountID}, nil
}

func tokenFingerprint(accessToken string) string {
	sum := sha256.Sum256([]byte(accessToken))
	return hex.EncodeToString(sum[:])
}

func chatGPTError(err error) error {
	var auth *chatgptauth.AuthError
	if !errors.As(err, &auth) {
		return errors.New("could not reach ChatGPT. Check the connection and try again")
	}
	switch auth.Code {
	case "expired":
		return errors.New("the sign-in code expired. Start again")
	case "authorization_failed":
		return errors.New("ChatGPT did not approve the sign-in. Start again")
	case "reconnect_required", "account_changed":
		return errors.New("ChatGPT needs you to sign in again (Settings → Models)")
	case "missing_account":
		return errors.New("this ChatGPT account has no subscription Rex can use")
	default:
		return errors.New("ChatGPT sign-in failed (" + auth.Code + "). Try again")
	}
}

// refreshChatGPTCredential answers the runtime when a run's ChatGPT token
// is about to expire or was rejected.
func (h *Host) refreshChatGPTCredential(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CredentialFingerprint string `json:"credential_fingerprint"`
		AppID                 string `json:"app_id"`
		RunID                 string `json:"run_id"`
		HostRunID             string `json:"host_run_id"`
		ConnectionID          string `json:"connection_id"`
		Provider              string `json:"provider"`
		AccountID             string `json:"account_id"`
		Reason                string `json:"reason"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.AppID != AppID || input.Provider != "openai_chatgpt" || input.ConnectionID != chatGPTConnectionID {
		writeError(w, http.StatusUnauthorized, errors.New("unknown credential"))
		return
	}
	credential, err := h.Settings.ChatGPT.Credential(r.Context(), input.Reason == "unauthorized", input.CredentialFingerprint)
	if err != nil || credential.AccountID != input.AccountID {
		writeError(w, http.StatusUnauthorized, errors.New("sign in to ChatGPT again"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credential": credential})
}

func (h *Host) chatGPTStatus(w http.ResponseWriter, r *http.Request) {
	status, err := h.Settings.ChatGPT.Status()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *Host) chatGPTStartLogin(w http.ResponseWriter, r *http.Request) {
	status, err := h.Settings.ChatGPT.StartLogin(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *Host) chatGPTPollLogin(w http.ResponseWriter, r *http.Request) {
	status, err := h.Settings.ChatGPT.PollLogin(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *Host) chatGPTSignOut(w http.ResponseWriter, r *http.Request) {
	if err := h.Settings.ChatGPT.SignOut(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, ChatGPTStatus{})
}
