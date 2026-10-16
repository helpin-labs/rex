package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSandboxRemovalCLIUsesExplicitDirectory(t *testing.T) {
	if os.Getenv("REX_REMOVAL_TEST_CHILD") == "1" {
		os.Args = []string{os.Args[0], "--remove-sandbox", os.Getenv("REX_REMOVAL_TEST_DIRECTORY")}
		main()
		return
	}
	data := filepath.Join(t.TempDir(), "removal directory with spaces")
	foreign := t.TempDir()
	marker := filepath.Join(foreign, "keep.txt")
	if err := os.WriteFile(marker, []byte("kept"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(data, "wsl"), 0700); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(data, "wsl", "installation.json")
	raw, err := json.Marshal(map[string]any{"Active": map[string]string{"Name": "Ubuntu", "Nonce": strings.Repeat("a", 32), "Directory": foreign}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record, raw, 0600); err != nil {
		t.Fatal(err)
	}
	run := func() ([]byte, error) {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSandboxRemovalCLIUsesExplicitDirectory$")
		cmd.Env = append(os.Environ(), "REX_REMOVAL_TEST_CHILD=1", "REX_REMOVAL_TEST_DIRECTORY="+data, "REX_DATA_DIR="+foreign)
		return cmd.CombinedOutput()
	}
	if output, err := run(); err == nil || !strings.Contains(string(output), "outside Rex's managed directory") {
		t.Fatalf("foreign record was not rejected: %v %s", err, output)
	}
	if _, err := os.Stat(record); err != nil {
		t.Fatal("rejected ownership record was removed", err)
	}
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	if output, err := run(); err != nil {
		t.Fatalf("absent installation must be harmless: %v %s", err, output)
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "kept" {
		t.Fatal("environment override's data changed", err)
	}
}
