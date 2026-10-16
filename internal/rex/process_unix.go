//go:build unix

package rex

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func OwnProcessTree() error          { return nil }
func ConfigureProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func SignalProcessTree(cmd *exec.Cmd, force bool) error {
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	return syscall.Kill(-cmd.Process.Pid, signal)
}
func forwardBrowserProcess(real string, args, env []string) error {
	return syscall.Exec(real, append([]string{real}, args...), env)
}
func linkBrowserShim(self, link string) error { return os.Symlink(self, link) }

func ReapOrphan(pidFile, name string) {
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	_ = os.Remove(pidFile)
	var pid int
	if _, err := fmt.Sscan(string(raw), &pid); err != nil || pid <= 1 {
		return
	}
	output, err := exec.Command("ps", "-o", "comm=", "-p", fmt.Sprint(pid)).Output()
	if err != nil {
		return
	}
	executable := filepath.Base(strings.TrimSpace(string(output)))
	if executable != name && !strings.HasPrefix(executable, name+"-") {
		return
	}
	slog.Warn("stopping an owned process left behind by a previous run", "pid", pid, "name", name)
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	_ = syscall.Kill(pid, syscall.SIGTERM)
	time.Sleep(time.Second)
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

func revealPath(path string, selectFile bool) error {
	args := []string{path}
	if selectFile {
		args = []string{"-R", path}
	}
	return exec.Command("open", args...).Start()
}

// Unix data privacy is established by the host's existing 0700/0600 creation modes.
func SecureDataDirectory(path string) error { return nil }

func configureBrowserPipes(command *exec.Cmd, childRead, parentWrite, parentRead, childWrite *os.File) error {
	command.ExtraFiles = []*os.File{childRead, childWrite}
	return nil
}

func browserVersion(executable string) string {
	index := strings.Index(executable, ".app/")
	if index < 0 {
		return ""
	}
	output, err := exec.Command("/usr/libexec/PlistBuddy", "-c", "Print CFBundleShortVersionString", filepath.Join(executable[:index+4], "Contents", "Info.plist")).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
