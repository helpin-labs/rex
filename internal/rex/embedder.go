package rex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// Rex ships its own embedding model (Bekko Embedding v1 a8m: small,
// multilingual, 384 dimensions, no query or document prefixes) and a small
// llama.cpp server to run it, so memory search works without any model
// server and never depends on what LM Studio has loaded.
const (
	BundledEmbeddingModel = "bekko-embedding-v1-a8m"
	// Stored memories record the model ID they were embedded with; a new ID
	// rebuilds them (see rebuildIfEmbeddingChanged).
	bundledEmbeddingModelID = "rex-bundled/bekko-embedding-v1-a8m.Q8_0/v1"
	EmbeddingSourceBundled  = "bundled"
)

// Embedder supervises the bundled llama-server process.
type Embedder struct {
	Binary  string
	Model   string
	DataDir string
	Log     io.Writer

	mu      sync.Mutex
	cmd     *exec.Cmd
	url     string
	stopped bool
	exited  chan struct{}
}

// Available reports whether the binary and model exist.
func (e *Embedder) Available() error {
	if e == nil || e.Binary == "" || e.Model == "" {
		return errors.New("the bundled embedding model is not installed")
	}
	for _, path := range []string{e.Binary, e.Model} {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("the bundled embedding model is missing: %w", err)
		}
	}
	return nil
}

// URL is the OpenAI-compatible base URL, or "" while not running.
func (e *Embedder) URL() string {
	if e == nil {
		return ""
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.url
}

// Start launches the server and waits until it answers; it then keeps it
// running, restarting it if it exits unexpectedly.
func (e *Embedder) Start(ctx context.Context) error {
	if err := e.Available(); err != nil {
		return err
	}
	reapOrphan(filepath.Join(e.DataDir, "embedder.pid"), "llama-server")
	if err := e.launch(ctx); err != nil {
		return err
	}
	go e.supervise(ctx)
	return nil
}

func (e *Embedder) launch(ctx context.Context) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	// Bekko is a ModernBERT encoder: mean pooling, L2-normalised output,
	// inputs up to 8192 tokens (one batch must hold a whole input).
	cmd := exec.Command(e.Binary, "-m", e.Model, "--embedding", "--pooling", "mean", "--embd-normalize", "2",
		"--host", "127.0.0.1", "--port", fmt.Sprint(port),
		"-c", "8192", "-ub", "8192", "-ngl", "99", "--threads", "4", "--no-webui", "--alias", BundledEmbeddingModel)
	cmd.Stdout, cmd.Stderr = e.Log, e.Log
	ConfigureProcess(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start the embedding model: %w", err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	_ = os.WriteFile(filepath.Join(e.DataDir, "embedder.pid"), []byte(fmt.Sprint(cmd.Process.Pid)), 0o600)
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	readyCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for {
		request, _ := http.NewRequestWithContext(readyCtx, http.MethodGet, url+"/health", nil)
		if response, err := http.DefaultClient.Do(request); err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		select {
		case <-exited:
			return errors.New("the embedding model exited while starting")
		case <-readyCtx.Done():
			_ = cmd.Process.Kill()
			return errors.New("the embedding model did not start in time")
		case <-time.After(200 * time.Millisecond):
		}
	}
	e.mu.Lock()
	e.cmd, e.url, e.exited = cmd, url+"/v1", exited
	e.mu.Unlock()
	slog.Info("bundled embedding model ready", "url", url, "pid", cmd.Process.Pid)
	return nil
}

func (e *Embedder) supervise(ctx context.Context) {
	failures := 0
	for {
		e.mu.Lock()
		exited := e.exited
		e.mu.Unlock()
		select {
		case <-ctx.Done():
			e.Stop()
			return
		case <-exited:
		}
		e.mu.Lock()
		stopped := e.stopped
		e.url = ""
		e.mu.Unlock()
		if stopped {
			return
		}
		failures++
		slog.Error("bundled embedding model exited; restarting", "failures", failures)
		if failures > 5 {
			return
		}
		time.Sleep(time.Duration(failures) * time.Second)
		if err := e.launch(ctx); err != nil {
			slog.Error("could not restart the embedding model", "error", err)
			return
		}
	}
}

// Stop terminates the server.
func (e *Embedder) Stop() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.stopped = true
	cmd, exited := e.cmd, e.exited
	e.url = ""
	e.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = SignalProcessTree(cmd, false)
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		_ = SignalProcessTree(cmd, true)
	}
	_ = os.Remove(filepath.Join(e.DataDir, "embedder.pid"))
}
