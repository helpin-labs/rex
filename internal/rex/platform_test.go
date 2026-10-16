package rex

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestNativeExecutableNamesAndIsolation(t *testing.T) {
	name := ExecutableName("agent-runtime")
	if runtime.GOOS == "windows" {
		if name != "agent-runtime.exe" || ExecutableName(name) != name || !strings.HasSuffix(RuntimeTriple(), "-pc-windows-msvc") {
			t.Fatalf("wrong Windows sidecar names: %s %s", name, RuntimeTriple())
		}
		if ExecutionIsolation() != "none" {
			t.Fatal("unverified Windows commands must stay disabled")
		}
	} else if name != "agent-runtime" {
		t.Fatal("changed Unix executable name")
	}
}

func TestWindowsEnvironmentKeepsPathsWithoutProviderSecrets(t *testing.T) {
	t.Setenv("USERPROFILE", `C:\Users\Rex User`)
	t.Setenv("LOCALAPPDATA", `C:\Users\Rex User\AppData\Local`)
	t.Setenv("PATH", `C:\Windows\System32;C:\Program Files\Git\cmd`)
	t.Setenv("OPENAI_API_KEY", "must-not-reach-runtime")
	values := WindowsEnvironment()
	joined := strings.Join(values, "\n")
	for _, key := range []string{"USERPROFILE", "LOCALAPPDATA", "PATH"} {
		if !strings.Contains(joined, key+"="+os.Getenv(key)) {
			t.Fatalf("missing %s", key)
		}
	}
	if strings.Contains(joined, "OPENAI_API_KEY") || strings.Contains(joined, "must-not-reach-runtime") {
		t.Fatal("provider secret leaked into child environment")
	}
}
