package rex

import (
	"os"
	"runtime"
	"strings"
)

func ExecutableName(name string) string {
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		return name + ".exe"
	}
	return name
}

func RuntimeTriple() string {
	arch := "x86_64"
	if runtime.GOARCH == "arm64" {
		arch = "aarch64"
	}
	switch runtime.GOOS {
	case "windows":
		return arch + "-pc-windows-msvc"
	case "darwin":
		return arch + "-apple-darwin"
	default:
		return arch + "-unknown-linux-gnu"
	}
}

// WSL commands stay unavailable until runtime verifies their confinement.
func ExecutionIsolation() string {
	if runtime.GOOS == "darwin" {
		return "seatbelt"
	}
	return "none"
}

func WindowsEnvironment() []string {
	var env []string
	for _, key := range []string{"SystemRoot", "WINDIR", "PATH", "PATHEXT", "COMSPEC", "TEMP", "TMP", "USERPROFILE", "USERNAME", "HOMEDRIVE", "HOMEPATH", "LOCALAPPDATA", "APPDATA", "ProgramData", "ProgramFiles", "ProgramFiles(x86)"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func reapOrphan(path, name string) { ReapOrphan(path, name) }
