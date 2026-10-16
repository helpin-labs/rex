// Command rex-host is Rex's local backend. The desktop shell starts it as a
// sidecar; it owns product state and supervises the agent-runtime process.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/helpin-ai/rex/internal/rex"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--remove-sandbox" {
		dataDir, err := resolveDataDir()
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			err = rex.NewCommandSandbox(dataDir, "").Remove(ctx)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	// Linked as agent-browser, rex-host opens a bot's browser on first use
	// and hands the command to the real agent-browser.
	if strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") == rex.BrowserShimName {
		if err := rex.RunBrowserShim(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "agent-browser:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("rex host stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	if err := rex.OwnProcessTree(); err != nil {
		return err
	}
	dataDir, err := resolveDataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	if err := rex.SecureDataDirectory(dataDir); err != nil {
		return fmt.Errorf("secure data directory: %w", err)
	}
	logFile, err := os.OpenFile(filepath.Join(dataDir, "rex.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer logFile.Close()
	// When the desktop shell dies, writes to our stderr pipe fail. Ignore
	// SIGPIPE so the host survives long enough to stop the runtime, and keep
	// logging to the file even if stderr is gone.
	signal.Ignore(syscall.SIGPIPE)
	slog.SetDefault(slog.New(slog.NewTextHandler(bestEffortWriter{primary: logFile, secondary: os.Stderr}, nil)))
	slog.Info("starting rex host", "data_dir", dataDir, "pid", os.Getpid())

	workspaceRoot := strings.TrimSpace(os.Getenv("REX_WORKSPACE_ROOT"))
	if workspaceRoot == "" {
		home, _ := os.UserHomeDir()
		workspaceRoot = filepath.Join(home, "Rex")
	}

	// Rex was called Dost: on the first launch, move Dost's data over. It
	// looks for Dost's folder next to the data folder, so test and
	// development data folders are left alone.
	if err := rex.MigrateFromDost(dataDir, workspaceRoot); err != nil {
		return fmt.Errorf("move Dost data to Rex: %w", err)
	}

	var secrets rex.Secrets = rex.LegacySecrets{
		Current: rex.KeychainSecrets{Service: firstNonEmpty(os.Getenv("REX_KEYCHAIN_SERVICE"), "ai.rex.desktop")},
		Legacy:  rex.KeychainSecrets{Service: rex.LegacyKeychainService},
	}
	switch os.Getenv("REX_SECRETS") {
	case "memory":
		secrets = rex.NewMemorySecrets()
	case "file":
		secrets = &rex.FileSecrets{Path: filepath.Join(dataDir, "secrets.dev.json")}
	}
	mcpSecret, err := persistentSecret(secrets, "internal.mcp_secret")
	if err != nil {
		return fmt.Errorf("keychain: %w", err)
	}
	modelCredentialKey, err := persistentSecret(secrets, "internal.model_credential_key")
	if err != nil {
		return err
	}
	mcpCredentialKey, err := persistentSecret(secrets, "internal.mcp_credential_key")
	if err != nil {
		return err
	}

	store, err := rex.OpenStore(filepath.Join(dataDir, "rex.db"))
	if err != nil {
		return err
	}
	defer store.Close()
	settings := rex.NewSettingsManager(store, secrets)
	memory := rex.NewMemoryService(filepath.Join(dataDir, "memory.db"), store, settings)
	defer memory.Close()
	skills := &rex.SkillLibrary{Dir: filepath.Join(workspaceRoot, "skills")}
	if err := skills.EnsureDefaults(); err != nil {
		slog.Warn("could not create skills folder", "error", err)
	}

	listener, err := net.Listen("tcp", firstNonEmpty(os.Getenv("REX_HOST_ADDR"), "127.0.0.1:18797"))
	if err != nil {
		return fmt.Errorf("listen on desktop API: %w", err)
	}
	hostURL := "http://" + listener.Addr().String()
	uiToken := firstNonEmpty(os.Getenv("REX_UI_TOKEN"), randomToken())
	runtimeToken := randomToken()
	cfg := rex.Config{DataDir: dataDir, WorkspaceRoot: workspaceRoot, HostURL: hostURL, InternalToken: randomToken(), UIToken: uiToken, MCPSecret: mcpSecret}
	runtimePort, err := freePort()
	if err != nil {
		return err
	}
	runtimeClient, err := rex.NewRuntimeClient("http://127.0.0.1:"+runtimePort, runtimeToken)
	if err != nil {
		return err
	}
	host := rex.NewHost(cfg, store, runtimeClient, settings, memory, skills)
	host.Sandbox = rex.NewCommandSandbox(dataDir, os.Getenv("REX_WSL_RESOURCES"))
	embedder := &rex.Embedder{Binary: resolveSidecar("REX_EMBEDDER_BINARY", "llama-server"), Model: resolveEmbeddingModel(), DataDir: dataDir, Log: logFile}
	memory.SetEmbedder(embedder)
	if err := memory.Configure(context.Background()); err != nil {
		slog.Warn("memory unavailable", "error", err)
	}
	if err := writeConnectionFile(dataDir, hostURL, uiToken); err != nil {
		slog.Warn("could not write connection file", "error", err)
	}

	restartRequests := make(chan struct{}, 1)
	host.SetRestart(func() {
		select {
		case restartRequests <- struct{}{}:
		default:
		}
	})

	httpServer := &http.Server{Handler: host.Handler(), ReadHeaderTimeout: 10 * time.Second}
	serveErrors := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrors <- err
		}
	}()

	// Interrupts cancel this context, so a stop request is honoured even
	// while the host is waiting for the runtime to come up.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go host.RunWatcher(ctx)
	go host.RetroWorker(ctx)
	go host.CatalogWorker(ctx)
	go memory.Worker(ctx)
	go func() {
		if err := embedder.Start(ctx); err != nil {
			slog.Warn("bundled embedding model unavailable", "error", err)
		}
		if err := memory.Configure(ctx); err != nil {
			slog.Warn("memory unavailable", "error", err)
		}
	}()
	defer embedder.Stop()
	go host.Routines.Loop(ctx)

	parentClosed := make(chan struct{})
	if os.Getenv("REX_WATCH_STDIN") == "1" {
		// The desktop shell holds our stdin open. EOF means it exited,
		// including by crashing, so we must not linger as an orphan.
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			close(parentClosed)
		}()
	}

	reapOrphanedRuntime(dataDir)
	// Bot browsers open on first use through Rex's agent-browser wrapper;
	// without it they open at the start of each run, as before.
	browserShim, err := rex.BrowserShimEnv(dataDir, hostURL, cfg.InternalToken)
	if err != nil {
		slog.Warn("bot browsers open at the start of each run", "error", err)
		host.Browser.OpenEagerly = true
	}
	runtimeArgs := runtimeLaunch{sandbox: host.Sandbox, browserShim: browserShim, dataDir: dataDir, port: runtimePort, token: runtimeToken, hostURL: hostURL, internalToken: cfg.InternalToken,
		modelCredentialKey: modelCredentialKey, mcpCredentialKey: mcpCredentialKey, settings: settings, logFile: logFile}
	host.PrepareInterruptedRuns(ctx)
	command, done, err := runtimeArgs.start(ctx)
	if err != nil {
		return err
	}
	becomeReady := func() {
		readyCtx, cancelReady := context.WithTimeout(ctx, 60*time.Second)
		defer cancelReady()
		go func(exited <-chan struct{}) {
			select {
			case <-exited:
				cancelReady()
			case <-readyCtx.Done():
			}
		}(runtimeExited)
		if err := runtimeClient.WaitReady(readyCtx); err != nil {
			slog.Error("agent runtime did not become ready", "error", err)
			return
		}
		host.SetRuntimeReady(true)
		if err := host.Bootstrap(ctx); err != nil {
			slog.Error("bootstrap failed", "error", err)
		}
		host.Routines.Recover(ctx)
		slog.Info("rex is ready", "api", hostURL, "runtime", runtimeClient.BaseURL)
	}
	becomeReady()

	crashes := 0
	for {
		select {
		case <-ctx.Done():
			slog.Info("shutting down")
			return shutdown(httpServer, host, command, done)
		case <-parentClosed:
			slog.Info("desktop shell closed; shutting down")
			return shutdown(httpServer, host, command, done)
		case <-restartRequests:
			host.SetRuntimeReady(false)
			slog.Info("restarting agent runtime to apply settings")
			stopProcess(command, done, 8*time.Second)
			host.PrepareInterruptedRuns(ctx)
			command, done, err = runtimeArgs.start(ctx)
			if err != nil {
				slog.Error("could not restart agent runtime", "error", err)
				continue
			}
			becomeReady()
		case err := <-done:
			// The runtime crashed. Restart it a few times before giving up;
			// runs resume from their durable checkpoints.
			host.SetRuntimeReady(false)
			crashes++
			slog.Error("agent runtime exited", "error", err, "crashes", crashes)
			if crashes > 5 {
				_ = httpServer.Shutdown(context.Background())
				return fmt.Errorf("agent runtime keeps crashing: %w", err)
			}
			time.Sleep(time.Duration(crashes) * time.Second)
			host.PrepareInterruptedRuns(ctx)
			command, done, err = runtimeArgs.start(ctx)
			if err != nil {
				return err
			}
			becomeReady()
		case err := <-serveErrors:
			stopProcess(command, done, 5*time.Second)
			return fmt.Errorf("desktop API exited: %w", err)
		}
	}
}

// bestEffortWriter writes to the log file and, if still possible, stderr.
type bestEffortWriter struct{ primary, secondary io.Writer }

func (w bestEffortWriter) Write(p []byte) (int, error) {
	_, _ = w.secondary.Write(p)
	return w.primary.Write(p)
}

func shutdown(server *http.Server, host *rex.Host, command *exec.Cmd, done <-chan error) error {
	host.SetRuntimeReady(false)
	browserCtx, cancelBrowser := context.WithTimeout(context.Background(), 10*time.Second)
	host.Browser.CloseAll(browserCtx)
	cancelBrowser()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
	stopProcess(command, done, 8*time.Second)
	slog.Info("rex host stopped cleanly")
	return nil
}

type runtimeLaunch struct {
	sandbox                                      *rex.CommandSandbox
	dataDir, port, token, hostURL, internalToken string
	// browserShim routes the runtime's agent-browser calls through Rex.
	browserShim                          []string
	modelCredentialKey, mcpCredentialKey []byte
	settings                             *rex.SettingsManager
	logFile                              io.Writer
}

// runtimeExited closes when the current runtime process exits, so waiting
// for readiness stops early instead of timing out.
var runtimeExited <-chan struct{}

func (l runtimeLaunch) start(ctx context.Context) (*exec.Cmd, <-chan error, error) {
	binary, err := resolveRuntimeBinary()
	if err != nil {
		return nil, nil, err
	}
	settings, err := l.settings.Get(ctx)
	if err != nil {
		return nil, nil, err
	}
	endpoint := settings.LocalEndpoint()
	app := map[string]any{
		"app_id":           rex.AppID,
		"context_endpoint": l.hostURL + "/runtime/context",
		"context_token":    l.internalToken,
		// Skills come from Rex's library (~/Rex/skills) as folders.
		"skill_provider": map[string]any{
			"transport": "http", "base_url": l.hostURL + "/runtime/skills", "token": l.internalToken,
			"package_base_url": l.hostURL + "/runtime/skill-packages",
		},
		"workspace_provider": map[string]any{
			"transport": "http", "base_url": l.hostURL + "/runtime/workspaces", "token": l.internalToken,
		},
		// ChatGPT runs ask Rex for a fresh token before theirs expires.
		"memory_learning_callback":  map[string]any{"url": l.hostURL + "/runtime/memory/learning", "token_env": "REX_MODEL_CALLBACK_TOKEN"},
		"model_credential_callback": map[string]any{"url": l.hostURL + "/runtime/model-credentials/refresh", "token_env": "REX_MODEL_CALLBACK_TOKEN"},
		"model_endpoints": []map[string]any{{
			"id": endpoint.ID, "base_url": endpoint.BaseURL, "auth_mode": endpoint.AuthMode,
			"allow_http": strings.HasPrefix(endpoint.BaseURL, "http://"),
		}},
	}
	if settings.BrowserEnabled {
		app["browser"] = map[string]any{"enabled": true, "allowed_domains": []string{"*"}, "artifact_provider": map[string]any{"upload_endpoint": l.hostURL + "/runtime/artifacts", "token": l.internalToken}}
	}
	appConfig, err := json.Marshal(map[string]any{"apps": []any{app}})
	if err != nil {
		return nil, nil, err
	}
	env, err := l.settings.RuntimeEnv(ctx)
	if err != nil {
		return nil, nil, err
	}
	command := exec.Command(binary)
	command.Stdout = l.logFile
	command.Stderr = l.logFile
	command.Env = append(baseEnvironment(),
		"AGENT_RUNTIME_ADDR=127.0.0.1:"+l.port,
		"AGENT_RUNTIME_STORE_DRIVER=sqlite",
		"AGENT_RUNTIME_SQLITE_DSN="+filepath.Join(l.dataDir, "runtime.db"),
		"AGENT_RUNTIME_MEMORY_JOBS_SQLITE_PATH="+filepath.Join(l.dataDir, "memory-jobs.db"),
		"AGENT_RUNTIME_SERVICE_TOKEN="+l.token,
		"AGENT_RUNTIME_APP_CONFIG="+string(appConfig),
		"AGENT_RUNTIME_MODEL_CREDENTIAL_ENCRYPTION_KEY="+base64.StdEncoding.EncodeToString(l.modelCredentialKey),
		"REX_MODEL_CALLBACK_TOKEN="+l.internalToken,
		"AGENT_RUNTIME_MCP_CREDENTIAL_ENCRYPTION_KEY="+base64.StdEncoding.EncodeToString(l.mcpCredentialKey),
		"AGENT_RUNTIME_MCP_ALLOW_HTTP=true",
		"AGENT_RUNTIME_MCP_ALLOW_PRIVATE_NETWORKS=true",
		"AGENT_RUNTIME_BROWSER_ENABLED="+fmt.Sprint(settings.BrowserEnabled),
		"AGENT_RUNTIME_BROWSER_SESSION_TIMEOUT_SECONDS=1800",
		// Confine agent commands with macOS Seatbelt: writes only inside the
		// bot's workspace, no reads of credentials or Rex's own data.
		"AGENT_RUNTIME_EXECUTION_ISOLATION="+rex.ExecutionIsolation(),
		"AGENT_RUNTIME_SEATBELT_DENY_READ="+l.dataDir,
		// Rex owns this runtime alone, so runs cut off by a quit, crash or
		// settings restart are continued from their checkpoints.
		"AGENT_RUNTIME_RECOVER_INTERRUPTED_RUNS=true",
	)
	command.Env = append(command.Env,
		"AGENT_RUNTIME_BROWSER_PROFILE_ROOT="+filepath.Join(l.dataDir, "browser-profiles"),
		"AGENT_RUNTIME_BROWSER_PROFILE_APP_ID="+rex.AppID,
	)
	if executable := os.Getenv("REX_BROWSER_EXECUTABLE"); executable != "" {
		command.Env = append(command.Env, "AGENT_RUNTIME_BROWSER_EXTENSION_EXECUTABLE="+executable)
	}
	if l.sandbox != nil {
		command.Env = append(command.Env, l.sandbox.RuntimeEnv(ctx)...)
	}
	command.Env = append(command.Env, l.browserShim...)
	command.Env = append(command.Env, env...)
	// The bot's browser (installed Chrome/Brave/Edge, visible, with the bot's
	// profile) is launched by Rex before each run. The runtime must send
	// plain agent-browser commands: any launch option of its own, such as an
	// executable path, makes agent-browser relaunch the browser without the
	// bot's profile.
	rex.ConfigureProcess(command)
	if err := command.Start(); err != nil {
		return nil, nil, fmt.Errorf("start agent runtime: %w", err)
	}
	done := make(chan error, 1)
	exited := make(chan struct{})
	go func() {
		err := command.Wait()
		close(exited)
		done <- err
	}()
	runtimeExited = exited
	_ = os.WriteFile(filepath.Join(l.dataDir, "runtime.pid"), []byte(fmt.Sprint(command.Process.Pid)), 0o600)
	slog.Info("agent runtime started", "pid", command.Process.Pid, "port", l.port)
	return command, done, nil
}

// baseEnvironment passes a minimal environment to the runtime so the user's
// shell secrets are not inherited by tools.
func baseEnvironment() []string {
	if runtime.GOOS == "windows" {
		return rex.WindowsEnvironment()
	}
	var env []string
	for _, key := range []string{"HOME", "USER", "LOGNAME", "TMPDIR", "LANG", "LC_ALL", "SHELL"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	path := []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}
	if current := os.Getenv("PATH"); current != "" {
		path = append(path, current)
	}
	return append(env, "PATH="+strings.Join(path, ":"))
}

// stopProcess interrupts the runtime's process group, then kills it if it
// does not exit in time.
func stopProcess(command *exec.Cmd, done <-chan error, timeout time.Duration) {
	if command == nil || command.Process == nil {
		return
	}
	_ = rex.SignalProcessTree(command, false)
	select {
	case <-done:
		return
	case <-time.After(timeout):
	}
	_ = rex.SignalProcessTree(command, true)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

// reapOrphanedRuntime stops a runtime left behind when a previous host was
// killed without a chance to clean up (for example a forced quit).
func reapOrphanedRuntime(dataDir string) {
	rex.ReapOrphan(filepath.Join(dataDir, "runtime.pid"), "agent-runtime")
}

// resolveSidecar finds a bundled helper binary next to rex-host, as Tauri
// installs sidecars, unless an environment variable overrides it.
func resolveSidecar(envName, name string) string {
	if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
		return value
	}
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(executable)
	for _, candidate := range []string{filepath.Join(dir, rex.ExecutableName(name)), filepath.Join(dir, rex.ExecutableName(name+"-"+runtimeTriple())), filepath.Join(dir, "..", "src-tauri", "binaries", rex.ExecutableName(name+"-"+runtimeTriple()))} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

const embeddingModelFile = "bekko-embedding-v1-a8m-Q8_0.gguf"

// resolveEmbeddingModel finds the bundled embedding model: passed by the
// desktop shell, inside the app bundle, or in the source tree for development.
func resolveEmbeddingModel() string {
	if value := strings.TrimSpace(os.Getenv("REX_EMBEDDING_MODEL")); value != "" {
		return value
	}
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(executable)
	for _, candidate := range []string{
		filepath.Join(dir, "..", "Resources", "resources", "models", embeddingModelFile),
		filepath.Join(dir, "..", "src-tauri", "resources", "models", embeddingModelFile),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func resolveRuntimeBinary() (string, error) {
	if value := strings.TrimSpace(os.Getenv("AGENT_RUNTIME_BINARY")); value != "" {
		return value, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(executable)
	name := filepath.Base(executable)
	candidates := []string{filepath.Join(dir, rex.ExecutableName("agent-runtime"))}
	if suffix, ok := strings.CutPrefix(name, "rex-host-"); ok {
		candidates = append(candidates, filepath.Join(dir, "agent-runtime-"+suffix))
	}
	candidates = append(candidates, filepath.Join(dir, rex.ExecutableName("agent-runtime-"+runtimeTriple())))
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("bundled agent runtime not found next to %s", executable)
}

func runtimeTriple() string { return rex.RuntimeTriple() }

func resolveDataDir() (string, error) {
	if value := strings.TrimSpace(os.Getenv("REX_DATA_DIR")); value != "" {
		return value, nil
	}
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			return "", errors.New("Windows local application data directory is unavailable")
		}
		return filepath.Join(base, "ai.rex.desktop"), nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ai.rex.desktop"), nil
}

// writeConnectionFile lets development tools (and the Vite dev page) find
// the API. It is readable only by the user.
func writeConnectionFile(dataDir, hostURL, token string) error {
	payload, _ := json.Marshal(map[string]string{"url": hostURL, "token": token})
	return os.WriteFile(filepath.Join(dataDir, "connection.json"), payload, 0o600)
}

func persistentSecret(secrets rex.Secrets, key string) ([]byte, error) {
	value, err := secrets.Get(key)
	if err != nil {
		return nil, err
	}
	if decoded, err := hex.DecodeString(value); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	fresh := make([]byte, 32)
	if _, err := rand.Read(fresh); err != nil {
		return nil, err
	}
	if err := secrets.Set(key, hex.EncodeToString(fresh)); err != nil {
		return nil, err
	}
	return fresh, nil
}

func randomToken() string {
	value := make([]byte, 32)
	_, _ = rand.Read(value)
	return hex.EncodeToString(value)
}

func freePort() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer listener.Close()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	return port, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
