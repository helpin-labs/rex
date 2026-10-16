package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Handler returns the host's HTTP API: /api/* for the desktop webview,
// /runtime/* and /mcp/* for agent-runtime callbacks.
func (h *Host) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })

	mux.HandleFunc("POST /runtime/memory/learning", h.requireInternal(h.processMemoryLearning))
	mux.HandleFunc("POST /runtime/context", h.requireInternal(h.runtimeContext))
	mux.HandleFunc("POST /runtime/browser/ensure", h.requireInternal(h.ensureBrowser))
	mux.HandleFunc("POST /runtime/skills/by-id", h.requireInternal(h.lookupSkill))
	mux.HandleFunc("POST /runtime/skills/active-by-key", h.requireInternal(h.lookupSkill))
	mux.HandleFunc("GET /runtime/skill-packages/objects/{key...}", h.requireInternal(h.skillPackage))
	mux.HandleFunc("POST /runtime/workspaces/prepare", h.requireInternal(h.prepareWorkspace))
	mux.HandleFunc("POST /runtime/workspaces/finalize", h.requireInternal(h.finalizeWorkspace))
	mux.HandleFunc("POST /runtime/workspaces/cleanup", h.requireInternal(h.cleanupWorkspace))
	mux.HandleFunc("POST /runtime/artifacts", h.requireInternal(h.saveArtifact))
	mux.HandleFunc("POST /runtime/model-credentials/refresh", h.requireInternal(h.refreshChatGPTCredential))
	mux.Handle("/mcp/memory/", h.memoryMCP())
	mux.Handle("/mcp/setup/", h.setupMCP())
	mux.Handle("/mcp/bots/", h.botsMCP())
	mux.Handle("/mcp/google/", h.googleMCP())
	mux.Handle("/mcp/self/", h.selfMCP())
	mux.Handle("/mcp/retro/", h.retroMCP())
	mux.Handle("/mcp/catalog/", h.catalogMCP())
	mux.Handle("/mcp/logins/", h.loginsMCP())
	mux.HandleFunc("GET "+oauthCallbackPath, h.oauthCallback)

	api := http.NewServeMux()
	api.HandleFunc("GET /api/status", h.status)
	api.HandleFunc("GET /api/attention", h.attention)
	api.HandleFunc("GET /api/settings", h.getSettings)
	api.HandleFunc("PUT /api/settings", h.saveSettings)
	api.HandleFunc("POST /api/settings/test", h.testProvider)
	api.HandleFunc("GET /api/providers/{provider}/models", h.providerModels)
	api.HandleFunc("GET /api/chatgpt", h.chatGPTStatus)
	api.HandleFunc("POST /api/chatgpt/login", h.chatGPTStartLogin)
	api.HandleFunc("POST /api/chatgpt/login/poll", h.chatGPTPollLogin)
	api.HandleFunc("DELETE /api/chatgpt", h.chatGPTSignOut)

	api.HandleFunc("GET /api/bots", h.listBots)
	api.HandleFunc("POST /api/bots", h.saveBot)
	api.HandleFunc("POST /api/bots/draft", h.draftBot)
	api.HandleFunc("POST /api/bots/{id}/retrospective", h.lookBackNow)
	api.HandleFunc("GET /api/bots/{id}/retrospectives", h.listRetrospectives)
	api.HandleFunc("GET /api/skill-sources", h.listSkillSources)
	api.HandleFunc("POST /api/skill-sources", h.addSkillSource)
	api.HandleFunc("PATCH /api/skill-sources/{id}", h.updateSkillSource)
	api.HandleFunc("DELETE /api/skill-sources/{id}", h.removeSkillSource)
	api.HandleFunc("POST /api/skill-sources/{id}/refresh", h.refreshSkillSource)
	api.HandleFunc("GET /api/catalog", h.searchCatalog)
	api.HandleFunc("GET /api/catalog/preview", h.previewCatalogSkill)
	api.HandleFunc("POST /api/catalog/install", h.installCatalogSkill)
	api.HandleFunc("GET /api/bots/{id}/logins", h.listLogins)
	api.HandleFunc("POST /api/bots/{id}/logins", h.saveLogin)
	api.HandleFunc("PUT /api/bots/{id}/logins/{login}", h.saveLogin)
	api.HandleFunc("DELETE /api/bots/{id}/logins/{login}", h.deleteLogin)
	api.HandleFunc("GET /api/proposals", h.proposals)
	api.HandleFunc("POST /api/proposals/{id}", h.answerProposal)
	api.HandleFunc("PUT /api/bots/{id}", h.saveBot)
	api.HandleFunc("DELETE /api/bots/{id}", h.deleteBot)
	api.HandleFunc("GET /api/bots/{id}/files", h.listFiles)
	api.HandleFunc("GET /api/bots/{id}/file", h.readFile)
	api.HandleFunc("POST /api/bots/{id}/files", h.uploadFile)
	api.HandleFunc("POST /api/bots/{id}/reveal", h.revealFile)
	api.HandleFunc("POST /api/bots/{id}/browser/extension", h.setupBrowserExtension)
	api.HandleFunc("POST /api/bots/{id}/browser/takeover", h.browserTakeover)
	api.HandleFunc("POST /api/bots/{id}/browser/release", h.browserRelease)
	api.HandleFunc("GET /api/bots/{id}/browser/live", h.browserLive)
	api.HandleFunc("GET /api/browsers", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, DetectBrowsers()) })

	api.HandleFunc("GET /api/conversations", h.listConversations)
	api.HandleFunc("POST /api/conversations", h.createConversation)
	api.HandleFunc("GET /api/conversations/{id}", h.getConversation)
	api.HandleFunc("PATCH /api/conversations/{id}", h.renameConversation)
	api.HandleFunc("DELETE /api/conversations/{id}", h.deleteConversation)
	api.HandleFunc("POST /api/conversations/{id}/messages", h.sendMessage)
	api.HandleFunc("POST /api/conversations/{id}/fresh", h.startFresh)
	api.HandleFunc("POST /api/conversations/{id}/turns/{turn}/edit", h.editMessage)

	api.HandleFunc("POST /api/runs/{run_id}/interactions/{interaction_id}", h.respondInteraction)
	api.HandleFunc("GET /api/runs/{run_id}/events", h.streamRunEvents)
	api.HandleFunc("GET /api/runs/{run_id}/events/history", h.runEventHistory)
	api.HandleFunc("GET /api/runs/{run_id}/timeline", h.runTimeline)
	api.HandleFunc("POST /api/runs/{run_id}/cancel", h.cancelRun)

	api.HandleFunc("GET /api/memory", h.memoryOverview)
	api.HandleFunc("GET /api/memory/banks/{bank}", h.memorySources)
	api.HandleFunc("POST /api/memory/banks/{bank}/notes", h.memoryAddNote)
	api.HandleFunc("POST /api/memory/banks/{bank}/forget", h.memoryForget)
	api.HandleFunc("POST /api/memory/banks/{bank}/recall", h.memoryRecall)
	api.HandleFunc("POST /api/memory/banks/{bank}/consolidate", h.memoryConsolidate)
	api.HandleFunc("GET /api/memory/banks/{bank}/learning", h.memoryLearning)
	api.HandleFunc("POST /api/memory/banks/{bank}/history/search", h.memoryHistorySearch)
	api.HandleFunc("POST /api/memory/banks/{bank}/history/read", h.memoryHistoryRead)
	api.HandleFunc("POST /api/memory/turns/{id}/retry", h.memoryRetryTurn)

	api.HandleFunc("GET /api/skills", h.listSkills)
	api.HandleFunc("POST /api/skills", h.saveSkill)
	api.HandleFunc("PUT /api/skills/{id}", h.saveSkill)
	api.HandleFunc("DELETE /api/skills/{id}", h.deleteSkill)
	api.HandleFunc("POST /api/skills/import", h.importSkill)
	api.HandleFunc("POST /api/skills/reveal", h.revealSkills)

	api.HandleFunc("GET /api/routines", h.listRoutines)
	api.HandleFunc("POST /api/routines", h.saveRoutine)
	api.HandleFunc("PUT /api/routines/{id}", h.saveRoutine)
	api.HandleFunc("DELETE /api/routines/{id}", h.deleteRoutine)
	api.HandleFunc("POST /api/routines/{id}/run", h.runRoutineNow)
	api.HandleFunc("POST /api/routines/preview", h.previewSchedule)
	api.HandleFunc("POST /api/routine-runs/{id}/missed", h.answerMissed)
	api.HandleFunc("GET /api/routines/{id}/runs", h.routineHistory)

	api.HandleFunc("GET /api/connections", h.listConnections)
	api.HandleFunc("POST /api/connections", h.saveConnection)
	api.HandleFunc("PUT /api/connections/{id}", h.saveConnection)
	api.HandleFunc("DELETE /api/connections/{id}", h.deleteConnection)
	api.HandleFunc("POST /api/connections/{id}/oauth/start", h.startOAuth)
	api.HandleFunc("POST /api/connections/{id}/oauth/signout", h.signOut)
	api.HandleFunc("GET /api/google", h.getGoogle)
	api.HandleFunc("PUT /api/google", h.setGoogleClient)

	api.HandleFunc("POST /api/logs/reveal", h.revealLogs)
	mux.Handle("/api/", h.requireUI(api))
	return cors(recoverer(mux))
}

// requireUI authenticates the desktop webview. EventSource cannot set
// headers, so the token may also arrive as a query parameter.
func (h *Host) requireUI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !bearerMatches(r, h.cfg.UIToken) && !tokenParamMatches(r, h.cfg.UIToken) {
			writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func tokenParamMatches(r *http.Request, expected string) bool {
	got := r.URL.Query().Get("token")
	return expected != "" && got != "" && len(got) == len(expected) && got == expected
}

var allowedOrigins = map[string]bool{
	"tauri://localhost": true, "http://tauri.localhost": true, "https://tauri.localhost": true,
	"http://127.0.0.1:5180": true, "http://localhost:5180": true,
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); allowedOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if value := recover(); value != nil {
				slog.Error("request panic", "path", r.URL.Path, "error", value)
				writeError(w, http.StatusInternalServerError, errors.New("internal error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// --- status & settings ---

func (h *Host) status(w http.ResponseWriter, r *http.Request) {
	settings, _ := h.Settings.Get(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "runtime_ready": h.RuntimeReady(), "app_id": AppID,
		"data_dir": h.cfg.DataDir, "workspace_root": h.cfg.WorkspaceRoot,
		"memory":    map[string]any{"available": h.Memory.Available(), "status": h.Memory.Status()},
		"onboarded": settings.Onboarded, "active_runs": h.ActiveRuns(), "write_tools": h.WriteTools(),
		"web_search": settings.WebSearchReady(), "cloud_browsers": settings.CloudBrowsers(),
		"user_name": accountName(),
	})
}

// accountName is the Mac account's full name (or login name), shown on the
// profile button.
var accountName = sync.OnceValue(func() string {
	account, err := user.Current()
	if err != nil {
		return ""
	}
	return firstNonEmpty(strings.TrimSpace(account.Name), account.Username)
})

func (h *Host) getSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := h.Settings.Get(r.Context())
	writeResult(w, settings, err)
}

func (h *Host) saveSettings(w http.ResponseWriter, r *http.Request) {
	var input Settings
	if !decode(w, r, &input) {
		return
	}
	saved, restart, err := h.Settings.Save(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.Memory.Configure(r.Context()); err != nil {
		slog.Warn("memory configuration failed", "error", err)
	}
	writeJSON(w, http.StatusOK, saved)
	if restart {
		h.RequestRuntimeRestart()
	}
}

func (h *Host) providerModels(w http.ResponseWriter, r *http.Request) {
	models, err := h.ListModels(r.Context(), r.PathValue("provider"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func (h *Host) testProvider(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if !decode(w, r, &input) {
		return
	}
	result := h.TestProvider(r.Context(), input.Provider, input.Model)
	writeJSON(w, http.StatusOK, result)
}

// --- bots ---

func (h *Host) listBots(w http.ResponseWriter, r *http.Request) {
	bots, err := h.Store.ListBots(r.Context())
	writeResult(w, bots, err)
}

func (h *Host) saveBot(w http.ResponseWriter, r *http.Request) {
	var bot Bot
	if !decode(w, r, &bot) {
		return
	}
	// Editing a bot in the form ends its setup interview; the main bot
	// stays the main bot.
	bot.Setup = false
	if id := r.PathValue("id"); id != "" {
		bot.ID = id
		existing, err := h.Store.GetBot(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, errors.New("bot not found"))
			return
		}
		bot.Role = existing.Role
	} else {
		bot.Role = ""
		bot.ID = ""
		bots, _ := h.Store.ListBots(r.Context())
		bot.Position = len(bots)
	}
	if err := h.SaveBot(r.Context(), &bot); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, err := h.Store.ThreadForBot(r.Context(), bot); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, bot)
}

func (h *Host) deleteBot(w http.ResponseWriter, r *http.Request) {
	if bot, err := h.Store.GetBot(r.Context(), r.PathValue("id")); err == nil && bot.Role == RoleMain {
		writeError(w, http.StatusBadRequest, errors.New("Rex is the main bot and cannot be deleted"))
		return
	}
	// Logins go with the bot; their secrets are removed from the Keychain.
	if logins, err := h.Store.BotLogins(r.Context(), r.PathValue("id")); err == nil {
		for _, login := range logins {
			_ = h.DeleteLogin(r.Context(), login.BotID, login.ID)
		}
	}
	if err := h.Store.DeleteBot(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- workspace files ---

type WorkspaceFile struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

func (h *Host) botWorkspace(w http.ResponseWriter, r *http.Request) (*Bot, bool) {
	bot, err := h.Store.GetBot(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("bot not found"))
		return nil, false
	}
	return bot, true
}

// resolveInside joins a relative path to root and refuses escapes.
func resolveInside(root, relative string) (string, error) {
	cleaned := filepath.Clean("/" + strings.TrimSpace(relative))
	full := filepath.Join(root, cleaned)
	if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		return "", errors.New("path is outside the workspace")
	}
	return full, nil
}

func (h *Host) listFiles(w http.ResponseWriter, r *http.Request) {
	bot, ok := h.botWorkspace(w, r)
	if !ok {
		return
	}
	files := []WorkspaceFile{}
	root := bot.WorkspaceDir
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") || len(files) >= maxWalkedFiles {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		relative, _ := filepath.Rel(root, path)
		files = append(files, WorkspaceFile{Path: filepath.ToSlash(relative), Size: info.Size(), Modified: info.ModTime()})
		return nil
	})
	// The newest files are the ones worth seeing; a workspace can hold many.
	sort.Slice(files, func(i, j int) bool { return files[i].Modified.After(files[j].Modified) })
	total := len(files)
	if len(files) > maxListedFiles {
		files = files[:maxListedFiles]
	}
	writeJSON(w, http.StatusOK, map[string]any{"root": root, "files": files, "total": total})
}

const (
	maxWalkedFiles = 20000
	maxListedFiles = 500
)

func (h *Host) readFile(w http.ResponseWriter, r *http.Request) {
	bot, ok := h.botWorkspace(w, r)
	if !ok {
		return
	}
	path, err := resolveInside(bot.WorkspaceDir, r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	content, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("file not found"))
		return
	}
	if len(content) > 512<<10 || !utf8.Valid(content) {
		writeJSON(w, http.StatusOK, map[string]any{"binary": true, "size": len(content)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"content": string(content)})
}

// uploadFile stores an attachment in the bot's inbox/. Bots without file
// access cannot receive files; text attachments are inlined by the UI.
func (h *Host) uploadFile(w http.ResponseWriter, r *http.Request) {
	bot, ok := h.botWorkspace(w, r)
	if !ok {
		return
	}
	if !bot.UsesWorkspace() {
		writeError(w, http.StatusBadRequest, errors.New("this bot has no file access; turn on file access to attach files"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("upload: %w", err))
		return
	}
	defer file.Close()
	name := filepath.Base(strings.TrimSpace(header.Filename))
	if name == "." || name == "/" || name == "" || strings.HasPrefix(name, ".") {
		writeError(w, http.StatusBadRequest, errors.New("invalid file name"))
		return
	}
	if err := ensureWorkspace(bot.WorkspaceDir); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	target := filepath.Join(bot.WorkspaceDir, "inbox", name)
	for index := 2; ; index++ {
		if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
			break
		}
		extension := filepath.Ext(name)
		target = filepath.Join(bot.WorkspaceDir, "inbox", fmt.Sprintf("%s-%d%s", strings.TrimSuffix(name, extension), index, extension))
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	size, copyErr := io.Copy(out, file)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(target)
		writeError(w, http.StatusInternalServerError, errors.Join(copyErr, closeErr))
		return
	}
	relative, _ := filepath.Rel(bot.WorkspaceDir, target)
	writeJSON(w, http.StatusCreated, WorkspaceFile{Path: filepath.ToSlash(relative), Size: size, Modified: time.Now()})
}

func (h *Host) revealFile(w http.ResponseWriter, r *http.Request) {
	bot, ok := h.botWorkspace(w, r)
	if !ok {
		return
	}
	var input struct {
		Path string `json:"path"`
	}
	if !decode(w, r, &input) {
		return
	}
	path, err := resolveInside(bot.WorkspaceDir, input.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := ensureWorkspace(bot.WorkspaceDir); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := revealPath(path, path != bot.WorkspaceDir); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Host) revealLogs(w http.ResponseWriter, r *http.Request) {
	_ = revealPath(filepath.Join(h.cfg.DataDir, "rex.log"), true)
	w.WriteHeader(http.StatusNoContent)
}

// --- conversations ---

type ConversationDetail struct {
	Conversation *Conversation `json:"conversation"`
	Runs         []Run         `json:"runs"`
	Messages     []Message     `json:"messages"`
	Interactions []Interaction `json:"interactions"`
	Turns        []Turn        `json:"turns"`
}

// listConversations lists each bot's thread with its latest message.
func (h *Host) listConversations(w http.ResponseWriter, r *http.Request) {
	items, err := h.Store.ThreadSummaries(r.Context())
	writeResult(w, items, err)
}

func (h *Host) createConversation(w http.ResponseWriter, r *http.Request) {
	var input struct {
		BotID string `json:"bot_id"`
		Title string `json:"title"`
	}
	if !decode(w, r, &input) {
		return
	}
	bot, err := h.Store.GetBot(r.Context(), input.BotID)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("bot not found"))
		return
	}
	// A bot has one conversation; asking for a new one returns it.
	item, err := h.Store.ThreadForBot(r.Context(), *bot)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *Host) getConversation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	conversation, err := h.Store.GetConversation(ctx, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("conversation not found"))
		return
	}
	detail := ConversationDetail{Conversation: conversation, Runs: []Run{}, Messages: []Message{}, Interactions: []Interaction{}}
	detail.Turns, _ = h.Store.TurnsForConversation(ctx, conversation.ID)
	// A bot's thread holds many runs; only the active one can still change
	// or wait for the user. Earlier runs are read through their timelines.
	if h.RuntimeReady() && conversation.ActiveRunID != nil {
		if run, err := h.Runtime.GetRun(ctx, *conversation.ActiveRunID); err == nil {
			detail.Runs = append(detail.Runs, *run)
			if interactions, err := h.Runtime.ListInteractions(ctx, run.ID); err == nil {
				for _, interaction := range interactions {
					if interaction.Status == "pending" {
						detail.Interactions = append(detail.Interactions, interaction)
					}
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Host) renameConversation(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title string `json:"title"`
	}
	if !decode(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Title) == "" {
		writeError(w, http.StatusBadRequest, errors.New("title is required"))
		return
	}
	writeResult(w, map[string]string{"status": "ok"}, h.Store.RenameConversation(r.Context(), r.PathValue("id"), input.Title))
}

func (h *Host) deleteConversation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if conversation, err := h.Store.GetConversation(ctx, r.PathValue("id")); err == nil && conversation.ActiveRunID != nil {
		if run, err := h.Runtime.GetRun(ctx, *conversation.ActiveRunID); err == nil && !run.Terminal() {
			_, _ = h.Runtime.CancelRun(ctx, run.ID)
		}
	}
	if err := h.Store.DeleteConversation(ctx, r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Host) sendMessage(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Content string `json:"content"`
	}
	if !decode(w, r, &input) {
		return
	}
	run, turn, err := h.SendMessage(r.Context(), r.PathValue("id"), input.Content)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrBusy) {
			status = http.StatusConflict
		} else if errors.Is(err, ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run": run, "turn": turn})
}

func (h *Host) editMessage(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Content string `json:"content"`
	}
	if !decode(w, r, &input) {
		return
	}
	run, turn, err := h.EditLastMessage(r.Context(), r.PathValue("id"), r.PathValue("turn"), input.Content)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrBusy) {
			status = http.StatusConflict
		} else if errors.Is(err, ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run": run, "turn": turn})
}

func (h *Host) respondInteraction(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Intent  string `json:"intent"`
		Content string `json:"content"`
	}
	if !decode(w, r, &input) {
		return
	}
	run, err := h.Respond(r.Context(), r.PathValue("run_id"), r.PathValue("interaction_id"), input.Intent, input.Content)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (h *Host) cancelRun(w http.ResponseWriter, r *http.Request) {
	run, err := h.Cancel(r.Context(), r.PathValue("run_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (h *Host) runEventHistory(w http.ResponseWriter, r *http.Request) {
	events, err := h.Runtime.ListRunEvents(r.Context(), r.PathValue("run_id"))
	if events == nil {
		events = []RunEventEnvelope{}
	}
	writeResult(w, events, err)
}

// streamRunEvents proxies the runtime's SSE stream to the webview.
func (h *Host) streamRunEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}
	resp, err := h.Runtime.OpenRunEvents(r.Context(), r.PathValue("run_id"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		writeError(w, http.StatusBadGateway, fmt.Errorf("runtime event stream returned %d: %s", resp.StatusCode, strings.TrimSpace(string(payload))))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	buffer := make([]byte, 16<<10)
	for {
		count, readErr := resp.Body.Read(buffer)
		if count > 0 {
			if _, err := w.Write(buffer[:count]); err != nil {
				return
			}
			flusher.Flush()
		}
		if readErr != nil {
			return
		}
	}
}

// --- memory ---

type BankSummary struct {
	Bank   string   `json:"bank"`
	Label  string   `json:"label"`
	BotIDs []string `json:"bot_ids"`
}

func (h *Host) memoryOverview(w http.ResponseWriter, r *http.Request) {
	bots, err := h.Store.ListBots(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	banks := []BankSummary{}
	shared := BankSummary{Bank: sharedBank, Label: "Shared memory", BotIDs: []string{}}
	for _, bot := range bots {
		switch bot.MemoryMode {
		case MemoryPrivate:
			banks = append(banks, BankSummary{Bank: bot.MemoryBank(), Label: bot.Name, BotIDs: []string{bot.ID}})
		case MemoryShared:
			shared.BotIDs = append(shared.BotIDs, bot.ID)
		}
	}
	banks = append(banks, shared)
	writeJSON(w, http.StatusOK, map[string]any{"available": h.Memory.Available(), "status": h.Memory.Status(), "banks": banks})
}

func validBank(bank string) bool {
	return bank == sharedBank || (strings.HasPrefix(bank, "bot:") && len(bank) < 100)
}

func (h *Host) memorySources(w http.ResponseWriter, r *http.Request) {
	bank := r.PathValue("bank")
	if !validBank(bank) {
		writeError(w, http.StatusBadRequest, errors.New("unknown memory bank"))
		return
	}
	sources, err := h.Memory.Sources(r.Context(), bank)
	if sources == nil {
		sources = []MemorySource{}
	}
	writeResult(w, sources, err)
}

func (h *Host) memoryAddNote(w http.ResponseWriter, r *http.Request) {
	bank := r.PathValue("bank")
	var input struct {
		Content  string `json:"content"`
		Replaces string `json:"replaces"`
	}
	if !decode(w, r, &input) {
		return
	}
	if !validBank(bank) || strings.TrimSpace(input.Content) == "" {
		writeError(w, http.StatusBadRequest, errors.New("a memory bank and content are required"))
		return
	}
	note := &MemoryNote{Bank: bank, Content: strings.TrimSpace(input.Content), Replaces: strings.TrimSpace(input.Replaces)}
	if err := h.Store.CreateMemoryNote(r.Context(), note); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	h.Memory.Notify()
	writeJSON(w, http.StatusCreated, note)
}

func (h *Host) memoryForget(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DocumentID string `json:"document_id"`
	}
	if !decode(w, r, &input) {
		return
	}
	bank := r.PathValue("bank")
	if !validBank(bank) || input.DocumentID == "" {
		writeError(w, http.StatusBadRequest, errors.New("a memory bank and source are required"))
		return
	}
	if err := h.Memory.Forget(r.Context(), bank, input.DocumentID); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Host) memoryRecall(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Query string `json:"query"`
	}
	if !decode(w, r, &input) {
		return
	}
	results, err := h.Memory.Recall(r.Context(), r.PathValue("bank"), input.Query, 10)
	writeResult(w, map[string]any{"results": results}, err)
}

func (h *Host) memoryConsolidate(w http.ResponseWriter, r *http.Request) {
	result, err := h.Memory.Consolidate(r.Context(), r.PathValue("bank"))
	writeResult(w, result, err)
}

func (h *Host) memoryRetryTurn(w http.ResponseWriter, r *http.Request) {
	turn, err := h.Store.GetTurn(r.Context(), r.PathValue("id"))
	if err == nil && turn.MemoryStatus != "forgotten" {
		err = h.Store.QueueEpisode(r.Context(), *turn)
		if err == nil {
			err = h.Store.retryEpisode(r.Context(), firstNonEmpty(turn.MemoryBank, "bot:"+turn.BotID), turn.ID)
		}
	} else if err == nil {
		err = errors.New("source was forgotten")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.Memory.retryLearning(r.Context(), firstNonEmpty(turn.MemoryBank, "bot:"+turn.BotID), episodeDocument(turn.ID)); err != nil {
		writeError(w, 503, err)
		return
	}
	h.Memory.Notify()
	w.WriteHeader(http.StatusNoContent)
}

// --- skills ---

func (h *Host) listSkills(w http.ResponseWriter, r *http.Request) {
	skills, err := h.Skills.List()
	writeResult(w, skills, err)
}

func (h *Host) saveSkill(w http.ResponseWriter, r *http.Request) {
	var input Skill
	if !decode(w, r, &input) {
		return
	}
	if id := r.PathValue("id"); id != "" {
		input.ID = id
	}
	skill, err := h.Skills.Save(input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	h.resyncBotsUsingSkill(r.Context(), skill.ID)
	writeJSON(w, http.StatusOK, skill)
}

// importSkill adds a skill from an uploaded zip.
func (h *Host) importSkill(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSkillBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("a skill zip can be at most %d MB", maxSkillBytes>>20))
		return
	}
	skill, err := h.Skills.Import(data)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	h.resyncBotsUsingSkill(r.Context(), skill.ID)
	writeJSON(w, http.StatusOK, skill)
}

// revealSkills opens the skills folder in Finder.
func (h *Host) revealSkills(w http.ResponseWriter, r *http.Request) {
	if err := revealPath(h.Skills.Dir, false); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Host) deleteSkill(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.Skills.Delete(id); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	h.resyncBotsUsingSkill(r.Context(), id)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Host) resyncBotsUsingSkill(ctx context.Context, skillID string) {
	bots, _ := h.Store.ListBots(ctx)
	for _, bot := range bots {
		for _, id := range bot.Skills {
			if id == skillID && h.RuntimeReady() {
				_ = h.SyncBot(ctx, bot)
			}
		}
	}
}

// --- routines ---

func (h *Host) listRoutines(w http.ResponseWriter, r *http.Request) {
	routines, err := h.Store.ListRoutines(r.Context())
	writeResult(w, routines, err)
}

func (h *Host) saveRoutine(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var input Routine
	if !decode(w, r, &input) {
		return
	}
	if id := r.PathValue("id"); id != "" {
		existing, err := h.Store.GetRoutine(ctx, id)
		if err != nil {
			writeError(w, http.StatusNotFound, errors.New("routine not found"))
			return
		}
		input.ID, input.CreatedAt, input.ConversationID = id, existing.CreatedAt, existing.ConversationID
		scheduleChanged := existing.ScheduleKind != input.ScheduleKind || existing.IntervalMinutes != input.IntervalMinutes || existing.DailyTime != input.DailyTime
		if !scheduleChanged && existing.Enabled && input.Enabled {
			input.NextRunAt = existing.NextRunAt
		}
		if input.Enabled {
			input.PausedReason, input.ConsecutiveFailures = "", 0
		} else {
			input.PausedReason = existing.PausedReason
		}
	} else {
		input.ID, input.ConversationID = "", ""
	}
	if err := input.Validate(time.Now()); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	bot, err := h.Store.GetBot(ctx, input.BotID)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("bot not found"))
		return
	}
	if input.Enabled {
		if count, _ := h.Store.CountEnabledRoutines(ctx, input.ID); count >= MaxEnabledRoutines {
			writeError(w, http.StatusBadRequest, fmt.Errorf("at most %d routines can be enabled", MaxEnabledRoutines))
			return
		}
	}
	if !input.Enabled {
		input.NextRunAt = nil
	}
	if input.ConversationID == "" {
		conversation, err := h.Store.CreateConversation(ctx, bot.ID, "⏰ "+input.Title, "routine")
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		input.ConversationID = conversation.ID
	}
	if err := h.Store.SaveRoutine(ctx, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	saved, err := h.Store.GetRoutine(ctx, input.ID)
	writeResult(w, saved, err)
}

func (h *Host) deleteRoutine(w http.ResponseWriter, r *http.Request) {
	writeResult(w, map[string]string{"status": "deleted"}, h.Store.DeleteRoutine(r.Context(), r.PathValue("id")))
}

func (h *Host) runRoutineNow(w http.ResponseWriter, r *http.Request) {
	claim, err := h.Routines.RunNow(r.Context(), r.PathValue("id"))
	if err != nil && claim == nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, claim)
}

func (h *Host) routineHistory(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	runs, err := h.Store.RoutineRuns(r.Context(), r.PathValue("id"), limit)
	writeResult(w, runs, err)
}

// --- MCP connections ---

func (h *Host) listConnections(w http.ResponseWriter, r *http.Request) {
	items, err := h.ListMCPConnections(r.Context())
	writeResult(w, items, err)
}

func (h *Host) saveConnection(w http.ResponseWriter, r *http.Request) {
	var input MCPConnection
	if !decode(w, r, &input) {
		return
	}
	input.ID = r.PathValue("id")
	item, err := h.SaveMCPConnection(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *Host) deleteConnection(w http.ResponseWriter, r *http.Request) {
	writeResult(w, map[string]string{"status": "deleted"}, h.DeleteMCPConnection(r.Context(), r.PathValue("id")))
}

// --- browser ---

func (h *Host) browserTakeover(w http.ResponseWriter, r *http.Request) {
	result, err := h.Browser.TakeOver(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Host) browserLive(w http.ResponseWriter, r *http.Request) {
	view, err := h.Browser.Live(r.Context(), r.PathValue("id"))
	writeResult(w, view, err)
}

func (h *Host) browserRelease(w http.ResponseWriter, r *http.Request) {
	if err := h.Browser.Release(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"held": false})
}

// --- helpers ---

func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	if err := decoder.Decode(value); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request: %w", err))
		return false
	}
	return true
}

func writeResult(w http.ResponseWriter, value any, err error) {
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
