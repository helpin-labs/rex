package rex

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/net/publicsuffix"
)

// Bot logins let a bot sign in to sites the user chose without ever seeing
// the password. Each login belongs to one bot; the password and optional 2FA
// secret live in the Keychain. The bot points at the form fields on the
// page and Rex types the values into the bot's own browser, only when that
// page is on the login's site. Tool inputs and results carry no secrets.

type BotLogin struct {
	ID         string     `json:"id"`
	BotID      string     `json:"bot_id"`
	Site       string     `json:"site"`
	Username   string     `json:"username"`
	HasTwoStep bool       `json:"has_2fa"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// LoginInput is what the user enters; secrets are write-only.
type LoginInput struct {
	Site         string `json:"site"`
	Username     string `json:"username"`
	Password     string `json:"password,omitempty"`
	TwoStepKey   string `json:"two_step_key,omitempty"`
	ClearTwoStep bool   `json:"clear_2fa,omitempty"`
}

type loginSecrets struct {
	Password   string `json:"password"`
	TwoStepKey string `json:"two_step_key,omitempty"`
}

func loginSecretKey(id string) string { return "login." + id }

// loginSite reduces "https://www.momondo.se/flights" or "momondo.se" to the
// registrable domain, which subdomains such as accounts.momondo.se share.
func loginSite(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "", errors.New("enter the site, for example momondo.se")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return "", errors.New("enter the site, for example momondo.se")
	}
	domain, err := publicsuffix.EffectiveTLDPlusOne(parsed.Hostname())
	if err != nil {
		return "", fmt.Errorf("%q is not a site Rex can sign in to", parsed.Hostname())
	}
	return domain, nil
}

// --- store ---

func scanLogin(row interface{ Scan(...any) error }) (*BotLogin, error) {
	var item BotLogin
	err := row.Scan(&item.ID, &item.BotID, &item.Site, &item.Username, &item.HasTwoStep, &item.CreatedAt, &item.LastUsedAt)
	return &item, err
}

const loginColumns = `id,bot_id,site,username,has_2fa,created_at,last_used_at`

func (s *Store) BotLogins(ctx context.Context, botID string) ([]BotLogin, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+loginColumns+` FROM bot_logins WHERE bot_id=? ORDER BY site, username`, botID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []BotLogin{}
	for rows.Next() {
		item, err := scanLogin(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (s *Store) GetLogin(ctx context.Context, id string) (*BotLogin, error) {
	item, err := scanLogin(s.db.QueryRowContext(ctx, `SELECT `+loginColumns+` FROM bot_logins WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return item, err
}

func (s *Store) saveLogin(ctx context.Context, item *BotLogin) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO bot_logins(`+loginColumns+`) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET site=excluded.site, username=excluded.username, has_2fa=excluded.has_2fa, last_used_at=excluded.last_used_at`,
		item.ID, item.BotID, item.Site, item.Username, item.HasTwoStep, item.CreatedAt, item.LastUsedAt)
	return err
}

// --- host ---

// SaveLogin adds a login to a bot, or updates one when id is set. An empty
// password keeps the stored one.
func (h *Host) SaveLogin(ctx context.Context, botID, id string, input LoginInput) (*BotLogin, error) {
	if _, err := h.Store.GetBot(ctx, botID); err != nil {
		return nil, err
	}
	site, err := loginSite(input.Site)
	if err != nil {
		return nil, err
	}
	username := strings.TrimSpace(input.Username)
	if username == "" {
		return nil, errors.New("enter the username or email")
	}
	login := &BotLogin{ID: uuid.NewString(), BotID: botID, CreatedAt: time.Now().UTC()}
	secrets := loginSecrets{}
	if id != "" {
		existing, err := h.Store.GetLogin(ctx, id)
		if err != nil || existing.BotID != botID {
			return nil, ErrNotFound
		}
		login = existing
		if raw, _ := h.Settings.secrets.Get(loginSecretKey(id)); raw != "" {
			_ = json.Unmarshal([]byte(raw), &secrets)
		}
	}
	if input.Password != "" {
		secrets.Password = input.Password
	}
	if secrets.Password == "" {
		return nil, errors.New("enter the password")
	}
	switch {
	case input.ClearTwoStep:
		secrets.TwoStepKey = ""
	case strings.TrimSpace(input.TwoStepKey) != "":
		key, err := parseTwoStepKey(input.TwoStepKey)
		if err != nil {
			return nil, err
		}
		secrets.TwoStepKey = key.raw
	}
	login.Site, login.Username, login.HasTwoStep = site, username, secrets.TwoStepKey != ""
	payload, _ := json.Marshal(secrets)
	if err := h.Settings.secrets.Set(loginSecretKey(login.ID), string(payload)); err != nil {
		return nil, fmt.Errorf("store login in keychain: %w", err)
	}
	if err := h.Store.saveLogin(ctx, login); err != nil {
		return nil, err
	}
	return login, nil
}

func (h *Host) DeleteLogin(ctx context.Context, botID, id string) error {
	login, err := h.Store.GetLogin(ctx, id)
	if err != nil || login.BotID != botID {
		return ErrNotFound
	}
	if err := h.Settings.secrets.Delete(loginSecretKey(id)); err != nil {
		return err
	}
	_, err = h.Store.db.ExecContext(ctx, `DELETE FROM bot_logins WHERE id=?`, id)
	return err
}

// loginFor loads a login and its secrets for the bot that owns it.
func (h *Host) loginFor(ctx context.Context, botID, id string) (*BotLogin, loginSecrets, error) {
	var secrets loginSecrets
	login, err := h.Store.GetLogin(ctx, id)
	if err != nil || login.BotID != botID {
		return nil, secrets, errors.New("no such login for you; use list_logins")
	}
	raw, err := h.Settings.secrets.Get(loginSecretKey(id))
	if err != nil || raw == "" {
		return nil, secrets, errors.New("this login's password is missing; ask the user to add it again")
	}
	err = json.Unmarshal([]byte(raw), &secrets)
	return login, secrets, err
}

// KnownPasswords are redacted from text Rex keeps, such as memories.
func (h *Host) KnownPasswords(ctx context.Context) []string {
	rows, err := h.Store.db.QueryContext(ctx, `SELECT id FROM bot_logins`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			continue
		}
		var secrets loginSecrets
		if raw, _ := h.Settings.secrets.Get(loginSecretKey(id)); raw != "" && json.Unmarshal([]byte(raw), &secrets) == nil && len(secrets.Password) >= 4 {
			values = append(values, secrets.Password)
		}
	}
	return values
}

// browserForSignIn finds the bot's own browser session and checks that it
// shows a page on the login's site.
func (h *Host) browserForSignIn(ctx context.Context, botID string, login *BotLogin) (string, error) {
	if h.Browser.backend(ctx, botID) != "standard" {
		return "", errors.New("saved-password filling is available in Standard mode; ask the user to take over for Extension mode")
	}
	if h.Browser.Held(botID) {
		return "", errors.New("the user has taken over your browser; wait until they hand it back")
	}
	h.Browser.mu.Lock()
	runID := h.Browser.active[botID]
	h.Browser.mu.Unlock()
	if runID == "" {
		return "", errors.New("open the site's login page in your browser first")
	}
	session := browserSessionName(runID)
	if err := h.onLoginSite(ctx, session, login); err != nil {
		return "", err
	}
	return session, nil
}

// The browser calls sign-in makes; tests replace them.
var (
	browserPageURL   = currentURL
	browserFieldType = func(ctx context.Context, session, ref string) string {
		kind, _ := agentBrowser(ctx, "--session", session, "get", "attr", ref, "type")
		return strings.TrimSpace(kind)
	}
)

func (h *Host) onLoginSite(ctx context.Context, session string, login *BotLogin) error {
	page := browserPageURL(ctx, session)
	if page == "" {
		return errors.New("open the site's login page in your browser first")
	}
	parsed, err := url.Parse(page)
	if err != nil || parsed.Scheme != "https" {
		return errors.New("Rex only signs in on https pages")
	}
	site, err := loginSite(page)
	if err != nil || site != login.Site {
		return fmt.Errorf("this page is not on %s; Rex only enters this login on %s", login.Site, login.Site)
	}
	return nil
}

// browserBatch runs agent-browser commands with their arguments on stdin,
// so secrets never appear in a process's command line.
var browserBatch = func(ctx context.Context, session string, commands [][]string) error {
	payload, _ := json.Marshal(commands)
	cmd := exec.CommandContext(ctx, agentBrowserPath(), "--session", session, "batch", "--bail")
	cmd.Stdin = strings.NewReader(string(payload))
	// The output may echo what was typed, so it is never passed on.
	if err := cmd.Run(); err != nil {
		return errors.New("the browser could not fill those fields; take a new snapshot and check the refs")
	}
	return nil
}

// SignIn types a login into the bot's browser.
func (h *Host) SignIn(ctx context.Context, botID, loginID, usernameRef, passwordRef, submitRef string) (*BotLogin, error) {
	login, secrets, err := h.loginFor(ctx, botID, loginID)
	if err != nil {
		return nil, err
	}
	session, err := h.browserForSignIn(ctx, botID, login)
	if err != nil {
		return nil, err
	}
	for _, ref := range []string{usernameRef, passwordRef, submitRef} {
		if ref != "" && !strings.HasPrefix(ref, "@e") {
			return nil, errors.New("use element refs from your latest snapshot, like @e12")
		}
	}
	if passwordRef == "" {
		return nil, errors.New("point at the password field")
	}
	if browserFieldType(ctx, session, passwordRef) != "password" {
		return nil, errors.New("the password ref is not a password field; take a new snapshot")
	}
	commands := [][]string{}
	if usernameRef != "" {
		commands = append(commands, []string{"fill", usernameRef, login.Username})
	}
	commands = append(commands, []string{"fill", passwordRef, secrets.Password})
	if submitRef != "" {
		commands = append(commands, []string{"click", submitRef})
	}
	if err := browserBatch(ctx, session, commands); err != nil {
		return nil, err
	}
	h.touchLogin(ctx, login)
	return login, nil
}

// FillTwoStepCode types the current 2FA code into the bot's browser.
func (h *Host) FillTwoStepCode(ctx context.Context, botID, loginID, codeRef string, submitRef string) (*BotLogin, error) {
	login, secrets, err := h.loginFor(ctx, botID, loginID)
	if err != nil {
		return nil, err
	}
	if secrets.TwoStepKey == "" {
		return nil, errors.New("this login has no 2FA setup key; ask the user to enter the code or add the key in Edit bot → Logins")
	}
	key, err := parseTwoStepKey(secrets.TwoStepKey)
	if err != nil {
		return nil, err
	}
	session, err := h.browserForSignIn(ctx, botID, login)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(codeRef, "@e") || (submitRef != "" && !strings.HasPrefix(submitRef, "@e")) {
		return nil, errors.New("use element refs from your latest snapshot, like @e12")
	}
	commands := [][]string{{"fill", codeRef, key.code(time.Now())}}
	if submitRef != "" {
		commands = append(commands, []string{"click", submitRef})
	}
	if err := browserBatch(ctx, session, commands); err != nil {
		return nil, err
	}
	h.touchLogin(ctx, login)
	return login, nil
}

func (h *Host) touchLogin(ctx context.Context, login *BotLogin) {
	now := time.Now().UTC()
	login.LastUsedAt = &now
	_ = h.Store.saveLogin(ctx, login)
}

// --- 2FA codes (RFC 6238) ---

type twoStepKey struct {
	raw     string
	secret  []byte
	digits  int
	period  int64
	newHash func() hash.Hash
}

// parseTwoStepKey accepts the base32 setup key or an otpauth:// URI (what
// a QR code contains).
func parseTwoStepKey(raw string) (*twoStepKey, error) {
	raw = strings.TrimSpace(raw)
	key := &twoStepKey{raw: raw, digits: 6, period: 30, newHash: sha1.New}
	secret := raw
	if strings.HasPrefix(strings.ToLower(raw), "otpauth://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return nil, errors.New("this 2FA link is not valid")
		}
		query := parsed.Query()
		secret = query.Get("secret")
		if digits, err := strconv.Atoi(query.Get("digits")); err == nil && (digits == 6 || digits == 8) {
			key.digits = digits
		}
		if period, err := strconv.ParseInt(query.Get("period"), 10, 64); err == nil && period > 0 {
			key.period = period
		}
		switch strings.ToUpper(query.Get("algorithm")) {
		case "SHA256":
			key.newHash = sha256.New
		case "SHA512":
			key.newHash = sha512.New
		}
	}
	cleaned := strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(secret))
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimRight(cleaned, "="))
	if err != nil || len(decoded) < 10 {
		return nil, errors.New("this 2FA setup key is not valid")
	}
	key.secret = decoded
	return key, nil
}

func (k *twoStepKey) code(at time.Time) string {
	counter := make([]byte, 8)
	binary.BigEndian.PutUint64(counter, uint64(at.Unix()/k.period))
	mac := hmac.New(k.newHash, k.secret)
	mac.Write(counter)
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	modulo := uint32(1)
	for range k.digits {
		modulo *= 10
	}
	return fmt.Sprintf("%0*d", k.digits, value%modulo)
}

// --- tools ---

func (h *Host) LoginsToken(botID string) string {
	mac := hmac.New(sha256.New, h.cfg.MCPSecret)
	mac.Write([]byte("bot-logins:" + botID))
	return hex.EncodeToString(mac.Sum(nil))
}

func (h *Host) loginsMCPServer(bot Bot) RunMCPServer {
	return RunMCPServer{
		ServerID: "rex-logins", ServerName: "logins", Transport: "streamable_http",
		URL: h.cfg.HostURL + "/mcp/logins/" + bot.ID,
		// Signing in acts on the user's behalf: it follows the bot's approval policy.
		Tools:      []RunMCPTool{{Name: "list_logins", Access: "read"}, {Name: "sign_in", Access: "write"}, {Name: "fill_2fa_code", Access: "write"}},
		Credential: &RunMCPCredential{Type: "bearer_token", AccessToken: h.LoginsToken(bot.ID)},
	}
}

func (h *Host) loginsMCP() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		botID := strings.TrimPrefix(r.URL.Path, "/mcp/logins/")
		if botID == "" || !bearerMatches(r, h.LoginsToken(botID)) {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
			return
		}
		server := protocol.NewServer(&protocol.Implementation{Name: "rex-logins", Version: "1.0.0"}, nil)
		server.AddTool(&protocol.Tool{
			Name:        "list_logins",
			Description: "List the logins the user saved for you: site and username only. You never see passwords.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}},
		}, func(ctx context.Context, _ *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			logins, err := h.Store.BotLogins(ctx, botID)
			if err != nil {
				return toolError(err), nil
			}
			items := make([]map[string]any, 0, len(logins))
			for _, login := range logins {
				items = append(items, map[string]any{"login_id": login.ID, "site": login.Site, "username": login.Username, "has_2fa": login.HasTwoStep})
			}
			return toolResult(map[string]any{"logins": items}), nil
		})
		ref := map[string]any{"type": "string", "pattern": "^@e[0-9]+$"}
		server.AddTool(&protocol.Tool{
			Name: "sign_in",
			Description: "Sign in with a saved login on the page open in your browser. Take a snapshot first and pass the refs of the username field, " +
				"the password field and optionally the submit button. Rex types the values; it only does so on the login's own site.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"login_id", "password_ref"}, "properties": map[string]any{
				"login_id": map[string]any{"type": "string"}, "username_ref": ref, "password_ref": ref, "submit_ref": ref,
			}},
		}, func(ctx context.Context, request *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			var input struct {
				LoginID     string `json:"login_id"`
				UsernameRef string `json:"username_ref"`
				PasswordRef string `json:"password_ref"`
				SubmitRef   string `json:"submit_ref"`
			}
			if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
				return toolError(err), nil
			}
			login, err := h.SignIn(ctx, botID, input.LoginID, input.UsernameRef, input.PasswordRef, input.SubmitRef)
			if err != nil {
				return toolError(err), nil
			}
			return toolResult(map[string]any{"status": "filled", "message": "Entered the login for " + login.Username + " on " + login.Site + ". Take a snapshot to check the result."}), nil
		})
		server.AddTool(&protocol.Tool{
			Name:        "fill_2fa_code",
			Description: "Fill the current 2FA code of a saved login into the code field on the page. Only works when the user saved the login's 2FA setup key.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"login_id", "code_ref"}, "properties": map[string]any{
				"login_id": map[string]any{"type": "string"}, "code_ref": ref, "submit_ref": ref,
			}},
		}, func(ctx context.Context, request *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			var input struct {
				LoginID   string `json:"login_id"`
				CodeRef   string `json:"code_ref"`
				SubmitRef string `json:"submit_ref"`
			}
			if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
				return toolError(err), nil
			}
			login, err := h.FillTwoStepCode(ctx, botID, input.LoginID, input.CodeRef, input.SubmitRef)
			if err != nil {
				return toolError(err), nil
			}
			return toolResult(map[string]any{"status": "filled", "message": "Entered the 2FA code for " + login.Site + "."}), nil
		})
		protocol.NewStreamableHTTPHandler(func(*http.Request) *protocol.Server { return server }, &protocol.StreamableHTTPOptions{Stateless: true, JSONResponse: true}).ServeHTTP(w, r)
	})
}

// --- HTTP for the app ---

func (h *Host) listLogins(w http.ResponseWriter, r *http.Request) {
	items, err := h.Store.BotLogins(r.Context(), r.PathValue("id"))
	writeResult(w, items, err)
}

func (h *Host) saveLogin(w http.ResponseWriter, r *http.Request) {
	var input LoginInput
	if !decode(w, r, &input) {
		return
	}
	login, err := h.SaveLogin(r.Context(), r.PathValue("id"), r.PathValue("login"), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, login)
}

func (h *Host) deleteLogin(w http.ResponseWriter, r *http.Request) {
	if err := h.DeleteLogin(r.Context(), r.PathValue("id"), r.PathValue("login")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
