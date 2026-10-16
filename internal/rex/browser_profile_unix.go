//go:build !windows

package rex

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// stopProfileHolders terminates Chrome processes started with this exact
// profile folder. Only Rex's own bot profiles are ever passed here.
func stopProfileHolders(profile string) {
	output, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return
	}
	marker := "--user-data-dir=" + profile
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, marker) || strings.Contains(line, "--type=") {
			continue
		}
		if pid, _, ok := strings.Cut(line, " "); ok {
			_ = exec.Command("kill", pid).Run()
		}
	}
	time.Sleep(1500 * time.Millisecond)
	_ = os.Remove(filepath.Join(profile, "SingletonLock"))
}
