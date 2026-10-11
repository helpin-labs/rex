package rex

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"
)

type RuntimeBrowserStatus struct {
	Backend string `json:"backend"`
	Active  bool   `json:"active"`
	Held    bool   `json:"held"`
	RunID   string `json:"run_id,omitempty"`
}

func (c *RuntimeClient) BrowserControl(ctx context.Context, botID, action string) (RuntimeBrowserStatus, error) {
	method := http.MethodPost
	var input any = map[string]string{"action": action}
	if action == "status" {
		method = http.MethodGet
		input = nil
	}
	var status RuntimeBrowserStatus
	err := c.do(ctx, method, "/v1/browser/"+url.PathEscape(botID)+"?app_id="+AppID, input, &status)
	return status, err
}

func (b *BrowserControl) extensionControl(ctx context.Context, botID, action string) (RuntimeBrowserStatus, error) {
	status, err := b.host.Runtime.BrowserControl(ctx, botID, action)
	if err != nil {
		return status, err
	}
	b.mu.Lock()
	if status.Held {
		b.held[botID] = heldBrowser{RunID: status.RunID, Since: time.Now()}
	} else {
		delete(b.held, botID)
	}
	b.mu.Unlock()
	return status, nil
}

func (b *BrowserControl) takeOverExtension(ctx context.Context, botID string) (map[string]any, error) {
	status, err := b.extensionControl(ctx, botID, "takeover")
	if err != nil {
		return nil, err
	}
	return map[string]any{"held": status.Held, "message": "Use the bot's browser window, then hand it back. The page stays open."}, nil
}

func (h *Host) setupBrowserExtension(w http.ResponseWriter, r *http.Request) {
	botID := r.PathValue("id")
	bot, err := h.Store.GetBot(r.Context(), botID)
	settings, settingsErr := h.Settings.Get(r.Context())
	if err != nil || settingsErr != nil || bot.browserMode(settings) != "extension" {
		writeError(w, 400, errors.New("choose Extension mode for this bot first"))
		return
	}
	if _, err := h.Browser.extensionControl(r.Context(), botID, "open"); err != nil {
		writeError(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]string{"message": "The extension loads automatically in the bundled browser. Hand the browser back when ready."})
}

func (b *BrowserControl) extensionLive(ctx context.Context, botID string) (LiveView, error) {
	status, err := b.extensionControl(ctx, botID, "status")
	return LiveView{Active: status.Active, Held: status.Held, Backend: status.Backend}, err
}

func (b *BrowserControl) backend(ctx context.Context, botID string) string {
	b.mu.Lock()
	runID := b.active[botID]
	b.mu.Unlock()
	if runID != "" {
		if run, err := b.host.Runtime.GetRun(ctx, runID); err == nil {
			if policy, ok := run.Input.Metadata["runtime_browser_policy"].(map[string]interface{}); ok {
				if mode, ok := policy["backend"].(string); ok && mode != "" {
					return mode
				}
			}
		}
	}
	bot, _ := b.host.Store.GetBot(ctx, botID)
	settings, _ := b.host.Settings.Get(ctx)
	if bot == nil {
		return "extension"
	}
	return bot.browserMode(settings)
}
