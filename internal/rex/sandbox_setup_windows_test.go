package rex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWindowsWSLOwnershipRejectsForeignTargets(t *testing.T) {
	s := NewCommandSandbox(t.TempDir(), "")
	nonce := strings.Repeat("a", 32)
	owned := ownedDistribution{Name: "Rex-Sandbox-" + nonce, Nonce: nonce, Directory: filepath.Join(s.dataDir, "wsl", nonce)}
	if err := s.validateOwnership(owned); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []ownedDistribution{{Name: "Ubuntu", Nonce: nonce, Directory: owned.Directory}, {Name: owned.Name, Nonce: nonce, Directory: t.TempDir()}, {Name: owned.Name, Nonce: "../bad", Directory: owned.Directory}} {
		if s.validateOwnership(bad) == nil || s.owns(t.Context(), bad) {
			t.Fatalf("accepted foreign distribution: %+v", bad)
		}
	}
}

func TestWindowsWSLSetupRepairAndProbe(t *testing.T) {
	resources := os.Getenv("REX_WSL_E2E_ROOTFS")
	if resources == "" {
		t.Skip("requires the built rootfs and a real WSL2 kernel")
	}
	data := t.TempDir()
	if err := SecureDataDirectory(data); err != nil {
		t.Fatal(err)
	}
	s := NewCommandSandbox(data, resources)
	// All cleanup is scoped to the random distributions created by this test.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		installed, err := s.installation()
		if err != nil {
			return
		}
		for _, d := range append(installed.Retired, installed.Active) {
			if s.owns(ctx, d) {
				_, _ = runWSL(ctx, "--unregister", d.Name)
			}
		}
	})
	if err := s.install(t.Context()); err != nil {
		t.Fatal(err)
	}
	first, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	if !s.owns(t.Context(), first.Active) {
		t.Fatal("new distribution has no ownership proof")
	}
	if env := s.RuntimeEnv(t.Context()); len(env) != 4 || env[0] != "AGENT_RUNTIME_EXECUTION_ISOLATION=wsl" {
		t.Fatalf("verified sandbox disabled: %v %+v", env, s.Status())
	}
	if err := s.install(t.Context()); err != nil {
		t.Fatal(err)
	}
	second, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	if first.Active.Name == second.Active.Name || len(second.Retired) != 1 {
		t.Fatalf("repair did not stage a separate environment: %+v", second)
	}
	if !s.owns(t.Context(), first.Active) {
		t.Fatal("repair removed the previous environment before activation")
	}
	if env := s.RuntimeEnv(t.Context()); len(env) != 4 {
		t.Fatalf("repaired environment not enabled: %v", env)
	}
	s.resources = t.TempDir()
	if err := s.install(t.Context()); err == nil {
		t.Fatal("incomplete bundle installed")
	}
	unchanged, err := s.installation()
	if err != nil || unchanged.Active.Name != second.Active.Name {
		t.Fatalf("failed install replaced working sandbox: %+v %v", unchanged, err)
	}
}
