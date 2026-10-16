package rex

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/helpin-ai/agent-runtime/sandbox/wsl"
	"golang.org/x/sys/windows"
)

type ownedDistribution struct{ Name, Nonce, Directory string }
type sandboxInstallation struct {
	Active  ownedDistribution
	Retired []ownedDistribution
	SHA256  string
}
type sandboxBundle struct{ SHA256, Architecture string }

func initialSandboxStatus() CommandSandboxStatus {
	return CommandSandboxStatus{Supported: true, State: "not_installed", Message: "Set up the command sandbox to let bots run commands and Python in their folders."}
}

func runWSL(ctx context.Context, args ...string) ([]byte, error) {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, filepath.Join(system, "wsl.exe"), args...)
	command.Env = WindowsEnvironment()
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("WSL: %w: %s", err, strings.ReplaceAll(string(out), "\x00", ""))
	}
	return out, nil
}

func (s *CommandSandbox) installation() (sandboxInstallation, error) {
	var installed sandboxInstallation
	raw, err := os.ReadFile(filepath.Join(s.dataDir, "wsl", "installation.json"))
	if err != nil {
		return installed, err
	}
	if err := json.Unmarshal(raw, &installed); err != nil {
		return installed, err
	}
	if err := s.validateOwnership(installed.Active); err != nil {
		return installed, err
	}
	for _, retired := range installed.Retired {
		if err := s.validateOwnership(retired); err != nil {
			return installed, err
		}
	}
	return installed, nil
}

func (s *CommandSandbox) validateOwnership(d ownedDistribution) error {
	if len(d.Nonce) != 32 {
		return errors.New("invalid Rex WSL ownership record")
	}
	nonce, err := hex.DecodeString(d.Nonce)
	if err != nil || len(nonce) != 16 {
		return errors.New("invalid Rex WSL ownership nonce")
	}
	if d.Name != "Rex-Sandbox-"+d.Nonce || d.Directory != filepath.Join(s.dataDir, "wsl", d.Nonce) {
		return errors.New("WSL ownership record points outside Rex's managed directory")
	}
	return nil
}

func (s *CommandSandbox) owns(ctx context.Context, d ownedDistribution) bool {
	if s.validateOwnership(d) != nil {
		return false
	}
	out, err := runWSL(ctx, "--distribution", d.Name, "--user", "root", "--exec", "/bin/cat", "/etc/rex-owner")
	return err == nil && strings.TrimSpace(string(out)) == d.Nonce
}

func (s *CommandSandbox) bridge(d ownedDistribution) wsl.Config {
	return wsl.Config{Distribution: d.Name, Helper: "/opt/agent-runtime/sandbox", ProbeRoot: filepath.Join(s.dataDir, "wsl", "probes")}
}

func (s *CommandSandbox) RuntimeEnv(ctx context.Context) []string {
	disabled := []string{"AGENT_RUNTIME_EXECUTION_ISOLATION=none"}
	installed, err := s.installation()
	if os.IsNotExist(err) {
		return disabled
	}
	if err != nil {
		s.setStatus("error", err.Error(), false)
		return disabled
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if !s.owns(probeCtx, installed.Active) {
		s.setStatus("error", "Rex could not verify ownership of its Linux environment. Check WSL2, then use Set up or repair. If Windows asks for WSL installation, run wsl --install --no-distribution in an administrator terminal and restart Windows.", false)
		return disabled
	}
	config := s.bridge(installed.Active)
	if _, err := config.Probe(probeCtx); err != nil {
		s.setStatus("error", "Commands remain off: "+err.Error(), false)
		return disabled
	}
	// Runtime launch happens after the old native runtime has stopped.
	for _, old := range installed.Retired {
		if s.owns(probeCtx, old) {
			if _, err := runWSL(probeCtx, "--unregister", old.Name); err == nil {
				_ = os.RemoveAll(old.Directory)
			}
		}
	}
	s.setStatus("ready", "Commands and Python are confined to the bot's folder by WSL2 and Landlock.", false)
	return []string{"AGENT_RUNTIME_EXECUTION_ISOLATION=wsl", "AGENT_RUNTIME_WSL_DISTRIBUTION=" + config.Distribution, "AGENT_RUNTIME_WSL_HELPER=" + config.Helper, "AGENT_RUNTIME_WSL_PROBE_ROOT=" + config.ProbeRoot}
}

func (s *CommandSandbox) install(ctx context.Context) error {
	if s.resources == "" {
		return errors.New("this build does not contain the command sandbox; install a complete Windows build of Rex")
	}
	if _, err := runWSL(ctx, "--status"); err != nil {
		return errors.New("WSL2 is unavailable. In an administrator terminal run wsl --install --no-distribution, restart Windows if asked, then try again. Managed computers may require your administrator's approval")
	}
	raw, err := os.ReadFile(filepath.Join(s.resources, "rootfs.json"))
	if err != nil {
		return err
	}
	var bundle sandboxBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return err
	}
	if bundle.Architecture != runtime.GOARCH {
		return errors.New("the bundled command sandbox has the wrong architecture")
	}
	archive := filepath.Join(s.resources, "rootfs.tar")
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if len(bundle.SHA256) != 64 || hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(bundle.SHA256) {
		return errors.New("command sandbox checksum mismatch; reinstall Rex")
	}
	previous, err := s.installation()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return err
	}
	nonce := hex.EncodeToString(nonceBytes)
	target := ownedDistribution{Name: "Rex-Sandbox-" + nonce, Nonce: nonce, Directory: filepath.Join(s.dataDir, "wsl", nonce)}
	if err := os.MkdirAll(filepath.Dir(target.Directory), 0700); err != nil {
		return err
	}
	// Import never overwrites a registered distribution with the same name.
	if _, err := runWSL(ctx, "--import", target.Name, target.Directory, archive, "--version", "2"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _ = runWSL(cleanup, "--unregister", target.Name)
			_ = os.RemoveAll(target.Directory)
		}
	}()
	ownerFile, err := os.CreateTemp(filepath.Join(s.dataDir, "wsl"), "owner-")
	if err != nil {
		return err
	}
	defer os.Remove(ownerFile.Name())
	if _, err := ownerFile.WriteString(nonce); err != nil {
		ownerFile.Close()
		return err
	}
	if err := ownerFile.Close(); err != nil {
		return err
	}
	ownerPath, err := wsl.NativePath(ownerFile.Name())
	if err != nil {
		return err
	}
	if _, err := runWSL(ctx, "--distribution", target.Name, "--user", "root", "--exec", "/bin/cp", ownerPath, "/etc/rex-owner"); err != nil {
		return err
	}
	if _, err := s.bridge(target).Probe(ctx); err != nil {
		return fmt.Errorf("command sandbox verification failed: %w", err)
	}
	installed := sandboxInstallation{Active: target, SHA256: bundle.SHA256, Retired: previous.Retired}
	if previous.Active.Name != "" {
		installed.Retired = append(installed.Retired, previous.Active)
	}
	encoded, err := json.Marshal(installed)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Join(s.dataDir, "wsl"), "installation-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary.Name(), filepath.Join(s.dataDir, "wsl", "installation.json")); err != nil {
		return err
	}
	committed = true
	return nil
}
