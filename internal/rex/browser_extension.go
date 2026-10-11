package rex

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed browser_extension/*
var browserExtensionFiles embed.FS

const browserExtensionID = "nfhnaomjebdbbmjhcfhpidkkkfjlkbga"
const browserExtensionOrigin = "chrome-extension://" + browserExtensionID

type extensionCommand struct {
	ID      string   `json:"id"`
	Session string   `json:"session"`
	Args    []string `json:"args"`
	Epoch   string   `json:"epoch"`
	Expires int64    `json:"expires"`
	reply   chan json.RawMessage
	done    <-chan struct{}
}

type extensionBrowser struct {
	// gate serializes actions with takeover, so user and agent never act together.
	gate    sync.Mutex
	mu      sync.Mutex
	token   string
	epoch   string
	queue   chan *extensionCommand
	pending map[string]*extensionCommand
	seen    time.Time
	process *exec.Cmd
	exited  chan struct{}
}

func (b *BrowserControl) backend(ctx context.Context, botID string) string {
	b.mu.Lock()
	runID := b.active[botID]
	b.mu.Unlock()
	if runID != "" {
		if mode, _ := b.host.Store.GetSetting(ctx, "browser.backend."+browserSessionName(runID)); mode != "" {
			return mode
		}
	}
	bot, _ := b.host.Store.GetBot(ctx, botID)
	settings, _ := b.host.Settings.Get(ctx)
	if bot == nil {
		return "extension"
	}
	return bot.browserMode(settings)
}

func (b *BrowserControl) extension(ctx context.Context, botID string) (*extensionBrowser, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if existing := b.extensions[botID]; existing != nil {
		return existing, nil
	}
	if _, err := b.host.Store.GetBot(ctx, botID); err != nil {
		return nil, err
	}
	dir := filepath.Join(b.host.cfg.DataDir, "browser-extensions", Slug(botID))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	var config struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "bridge.json"))
	if err == nil {
		if err := json.Unmarshal(raw, &config); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if config.Token == "" {
		config.Token = randomHex(32)
	}
	config.URL = b.host.cfg.HostURL + "/extension/browser"
	parsed, err := url.Parse(config.URL)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" {
		return nil, errors.New("extension bridge requires a loopback host address")
	}
	entries, err := fs.ReadDir(browserExtensionFiles, "browser_extension")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".test.js") {
			continue
		}
		raw, err := browserExtensionFiles.ReadFile("browser_extension/" + entry.Name())
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), raw, 0600); err != nil {
			return nil, err
		}
	}
	raw, _ = json.Marshal(config)
	if err := os.WriteFile(filepath.Join(dir, "bridge.json"), raw, 0600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "config.js"), append([]byte("self.REX_BRIDGE = "), append(raw, ';')...), 0600); err != nil {
		return nil, err
	}
	ext := &extensionBrowser{token: config.Token, epoch: randomHex(8), queue: make(chan *extensionCommand), pending: map[string]*extensionCommand{}}
	b.extensions[botID] = ext
	return ext, nil
}

func extensionBrowserArgs(profile, page, extensionDir string) []string {
	return []string{"--user-data-dir=" + profile, "--no-first-run", "--no-default-browser-check", "--password-store=basic", "--use-mock-keychain", "--load-extension=" + extensionDir, "--disable-extensions-except=" + extensionDir, "--new-window", page}
}

func (b *BrowserControl) launchExtension(ctx context.Context, botID, page string, ext *extensionBrowser) error {
	ext.mu.Lock()
	defer ext.mu.Unlock()
	if ext.process != nil {
		select {
		case <-ext.exited:
			ext.process = nil
		default:
			return nil
		}
	}
	// Chrome for Testing supports loading our extension without manual setup.
	executable := bundledChrome()
	if executable == "" {
		return errors.New("install the bundled browser with agent-browser install")
	}
	profile := b.profileDir(botID)
	if err := os.MkdirAll(profile, 0700); err != nil {
		return err
	}
	// A backend switch starts a new browser; never attach to a Standard process.
	b.closeBotSessions(ctx, botID, "")
	if _, err := os.Lstat(filepath.Join(profile, "SingletonLock")); err == nil {
		return errors.New("close this bot's other browser window before starting Extension mode")
	}
	command := exec.Command(executable, extensionBrowserArgs(profile, page, filepath.Join(b.host.cfg.DataDir, "browser-extensions", Slug(botID)))...)
	if err := command.Start(); err != nil {
		return err
	}
	ext.process, ext.exited = command, make(chan struct{})
	exited := ext.exited
	go func() { _ = command.Wait(); close(exited) }()
	return nil
}

func (e *extensionBrowser) execute(ctx context.Context, session string, args []string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	command := &extensionCommand{ID: randomHex(16), Epoch: e.epoch, Session: session, Args: args, Expires: deadline.UnixMilli(), reply: make(chan json.RawMessage, 1), done: ctx.Done()}
	e.mu.Lock()
	e.pending[command.ID] = command
	e.mu.Unlock()
	defer func() { e.mu.Lock(); delete(e.pending, command.ID); e.mu.Unlock() }()
	select {
	case e.queue <- command:
	case <-ctx.Done():
		return nil, errors.New("Rex extension did not connect; close the bot's browser and retry")
	}
	select {
	case raw := <-command.reply:
		return raw, nil
	case <-ctx.Done():
		// Cancellation cannot retract an action already delivered to the browser.
		// Keep the takeover gate until its deadline or acknowledgement.
		timer := time.NewTimer(max(time.Until(deadline), 0))
		defer timer.Stop()
		select {
		case <-command.reply:
		case <-timer.C:
		}
		return nil, errors.New("extension command timed out; check the page before retrying an action")
	}
}

// extensionBridge accepts only a bot-specific credential from extension context.
// Web pages get neither this credential nor a message channel into the driver.
func (h *Host) extensionBridge(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" && origin != browserExtensionOrigin {
		writeError(w, 403, errors.New("invalid extension origin"))
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	var ext *extensionBrowser
	h.Browser.mu.Lock()
	for _, candidate := range h.Browser.extensions {
		if token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(candidate.token)) == 1 {
			ext = candidate
			break
		}
	}
	h.Browser.mu.Unlock()
	if ext == nil {
		writeError(w, 401, errors.New("unauthorized"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		ext.mu.Lock()
		ext.seen = time.Now()
		ext.mu.Unlock()
		timer := time.NewTimer(20 * time.Second)
		defer timer.Stop()
		for {
			select {
			case command := <-ext.queue:
				select {
				case <-command.done:
					continue
				default:
				}
				writeJSON(w, 200, command)
				return
			case <-timer.C:
				w.WriteHeader(204)
				return
			case <-r.Context().Done():
				return
			}
		}
	}
	var input struct {
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20)).Decode(&input); err != nil {
		writeError(w, 400, errors.New("invalid extension response"))
		return
	}
	var result struct {
		Success *bool `json:"success"`
	}
	if json.Unmarshal(input.Result, &result) != nil || result.Success == nil {
		writeError(w, 400, errors.New("invalid extension result"))
		return
	}
	ext.mu.Lock()
	command := ext.pending[input.ID]
	delete(ext.pending, input.ID)
	ext.mu.Unlock()
	if command == nil {
		writeError(w, 409, errors.New("expired extension command"))
		return
	}
	select {
	case <-command.done:
		writeError(w, 409, errors.New("expired extension command"))
	case command.reply <- input.Result:
		w.WriteHeader(204)
	default:
		writeError(w, 409, errors.New("duplicate extension result"))
	}
}

func (h *Host) browserDriver(w http.ResponseWriter, r *http.Request) {
	var input struct {
		AppID   string   `json:"app_id"`
		RunID   string   `json:"run_id"`
		Session string   `json:"session"`
		Action  string   `json:"action"`
		Command []string `json:"command"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.AppID != AppID || input.RunID == "" || input.Session != browserSessionName(input.RunID) {
		writeError(w, 403, errors.New("invalid browser scope"))
		return
	}
	conversation, err := h.Store.ConversationForRun(r.Context(), input.RunID)
	if err != nil {
		writeError(w, 403, errors.New("unknown browser run"))
		return
	}
	bot, err := h.Store.GetBot(r.Context(), conversation.BotID)
	if err != nil || !bot.Capabilities.Browser {
		writeError(w, 403, errors.New("browser access denied"))
		return
	}
	settings, err := h.Settings.Get(r.Context())
	if err != nil || !settings.BrowserEnabled {
		writeError(w, 403, errors.New("browsers are disabled"))
		return
	}
	key := "browser.backend." + input.Session
	mode, err := h.Store.GetSetting(r.Context(), key)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if mode == "" {
		mode = bot.browserMode(settings)
		if err := h.Store.SetSetting(r.Context(), key, mode); err != nil {
			writeError(w, 500, err)
			return
		}
	}
	if input.Action == "resolve" {
		writeJSON(w, 200, map[string]string{"backend": mode})
		return
	}
	if input.Action != "command" || mode != "extension" || len(input.Command) == 0 {
		writeError(w, 400, errors.New("invalid browser driver command"))
		return
	}
	h.Browser.mu.Lock()
	active := h.Browser.active[bot.ID]
	h.Browser.mu.Unlock()
	if active != input.RunID && input.Command[0] != "close" {
		writeError(w, 409, errors.New("browser belongs to a different run"))
		return
	}
	ext, err := h.Browser.extension(r.Context(), bot.ID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	ext.gate.Lock()
	defer ext.gate.Unlock()
	if h.Browser.Held(bot.ID) {
		writeError(w, 409, errors.New("the user holds this browser; wait until they hand it back"))
		return
	}
	switch input.Command[0] {
	case "open", "snapshot", "get", "click", "fill", "type", "select", "check", "uncheck", "scroll", "wait", "screenshot", "close":
	default:
		writeError(w, 400, errors.New("this operation is unavailable in Extension mode; ask the user to take over"))
		return
	}
	if input.Command[0] == "close" {
		writeJSON(w, 200, map[string]any{"success": true, "data": map[string]any{}})
		return
	}
	if err := h.Browser.launchExtension(r.Context(), bot.ID, "about:blank", ext); err != nil {
		writeError(w, 409, err)
		return
	}
	result, err := ext.execute(r.Context(), input.Session, input.Command)
	if err != nil {
		writeError(w, 504, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(result)
}

func (h *Host) setupBrowserExtension(w http.ResponseWriter, r *http.Request) {
	botID := r.PathValue("id")
	bot, err := h.Store.GetBot(r.Context(), botID)
	settings, settingsErr := h.Settings.Get(r.Context())
	if err != nil || settingsErr != nil || bot.browserMode(settings) != "extension" {
		writeError(w, 400, errors.New("choose Extension mode for this bot first"))
		return
	}
	if err := h.Browser.extensionIdle(r.Context(), botID); err != nil {
		writeError(w, 409, err)
		return
	}
	ext, err := h.Browser.extension(r.Context(), botID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	ext.gate.Lock()
	defer ext.gate.Unlock()
	if err := h.Browser.extensionIdle(r.Context(), botID); err != nil {
		writeError(w, 409, err)
		return
	}
	if err := h.Browser.launchExtension(r.Context(), botID, "about:blank", ext); err != nil {
		writeError(w, 409, err)
		return
	}
	h.Browser.mu.Lock()
	h.Browser.held[botID] = heldBrowser{Extension: true, RunID: h.Browser.active[botID], Since: time.Now()}
	h.Browser.mu.Unlock()
	writeJSON(w, 200, map[string]string{"message": "The extension loads automatically in the bundled browser. Hand the browser back when ready."})
}

func (b *BrowserControl) extensionIdle(ctx context.Context, botID string) error {
	bot, err := b.host.Store.GetBot(ctx, botID)
	if err != nil {
		return err
	}
	if !bot.Capabilities.Browser {
		return errors.New("this bot has no browser")
	}
	b.mu.Lock()
	runID := b.active[botID]
	b.mu.Unlock()
	if runID != "" {
		run, err := b.host.Runtime.GetRun(ctx, runID)
		if err != nil {
			return errors.New("could not check whether the bot is using its browser")
		}
		if run.Status == "running" || run.Status == "queued" {
			return errors.New("stop the bot or wait until it asks for help before taking over")
		}
	}
	return nil
}

func (b *BrowserControl) takeOverExtension(ctx context.Context, botID string) (map[string]any, error) {
	if err := b.extensionIdle(ctx, botID); err != nil {
		return nil, err
	}
	ext, err := b.extension(ctx, botID)
	if err != nil {
		return nil, err
	}
	ext.gate.Lock()
	defer ext.gate.Unlock()
	if err := b.extensionIdle(ctx, botID); err != nil {
		return nil, err
	}
	if err := b.launchExtension(ctx, botID, "about:blank", ext); err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.held[botID] = heldBrowser{Extension: true, RunID: b.active[botID], Since: time.Now()}
	b.mu.Unlock()
	return map[string]any{"held": true, "message": "Use the bot's browser window, then hand it back. The page stays open."}, nil
}

func (b *BrowserControl) closeExtensions(ctx context.Context) {
	b.mu.Lock()
	extensions := make([]*extensionBrowser, 0, len(b.extensions))
	for _, ext := range b.extensions {
		extensions = append(extensions, ext)
	}
	b.mu.Unlock()
	for _, ext := range extensions {
		ext.gate.Lock()
		ext.mu.Lock()
		process, exited, seen := ext.process, ext.exited, ext.seen
		ext.mu.Unlock()
		if process != nil {
			if time.Since(seen) < 30*time.Second {
				closeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				_, _ = ext.execute(closeCtx, "", []string{"close"})
				cancel()
			}
			select {
			case <-exited:
			case <-time.After(3 * time.Second):
				_ = process.Process.Signal(os.Interrupt)
			case <-ctx.Done():
				_ = process.Process.Signal(os.Interrupt)
			}
		}
		ext.gate.Unlock()
	}
}

func (b *BrowserControl) extensionLive(ctx context.Context, botID string) LiveView {
	b.mu.Lock()
	ext := b.extensions[botID]
	b.mu.Unlock()
	view := LiveView{Held: b.Held(botID), Backend: "extension"}
	if ext != nil {
		ext.mu.Lock()
		view.Active = time.Since(ext.seen) < 30*time.Second
		ext.mu.Unlock()
	}
	return view
}
