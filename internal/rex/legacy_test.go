package rex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateFromDostMovesDataAndPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	support := filepath.Join(home, "Library", "Application Support")
	oldDir := filepath.Join(support, LegacyDataDirName)
	newDir := filepath.Join(support, "ai.rex.desktop")
	oldWorkspace := filepath.Join(home, "Dost")
	newWorkspace := filepath.Join(home, "Rex")
	if err := os.MkdirAll(filepath.Join(oldWorkspace, "spain-trip"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(oldDir, "browser-profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(oldDir, "memory.db"), []byte("memories"), 0o600)

	ctx := context.Background()
	store, err := OpenStore(filepath.Join(oldDir, legacyDBName))
	if err != nil {
		t.Fatal(err)
	}
	main := &Bot{ID: "main", Name: "Dost", Role: RoleMain, Instructions: "You are Dost.", WorkspaceDir: filepath.Join(oldWorkspace, "dost")}
	trip := &Bot{ID: "trip", Name: "Spain trip", WorkspaceDir: filepath.Join(oldWorkspace, "spain-trip")}
	other := &Bot{ID: "other", Name: "Elsewhere", WorkspaceDir: filepath.Join(home, "Dostoevsky")}
	for _, bot := range []*Bot{main, trip, other} {
		if err := store.SaveBot(ctx, bot); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()

	// Rex's shell may create its folder before the host starts.
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := MigrateFromDost(newDir, newWorkspace); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(newDir, "rex.db"), filepath.Join(newDir, "memory.db"), filepath.Join(newDir, "browser-profiles"), filepath.Join(newWorkspace, "spain-trip"), filepath.Join(oldDir, migratedMarker)} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}
	if _, err := os.Stat(oldWorkspace); !os.IsNotExist(err) {
		t.Fatalf("~/Dost should have moved, got %v", err)
	}

	store, err = OpenStore(filepath.Join(newDir, "rex.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, _ := store.GetBot(ctx, main.ID)
	if got.Name != "Rex" || got.Instructions != "You are Rex." || got.WorkspaceDir != filepath.Join(newWorkspace, "dost") {
		t.Fatalf("main bot not moved: %+v", got)
	}
	got, _ = store.GetBot(ctx, trip.ID)
	if got.WorkspaceDir != filepath.Join(newWorkspace, "spain-trip") {
		t.Fatalf("bot folder not rewritten: %s", got.WorkspaceDir)
	}
	got, _ = store.GetBot(ctx, other.ID)
	if got.WorkspaceDir != filepath.Join(home, "Dostoevsky") {
		t.Fatalf("a folder that only starts with the same letters was rewritten: %s", got.WorkspaceDir)
	}

	// A second launch changes nothing.
	if err := MigrateFromDost(newDir, newWorkspace); err != nil {
		t.Fatal(err)
	}
}

func TestLegacySecretsCopiesFromDost(t *testing.T) {
	current, legacy := NewMemorySecrets(), NewMemorySecrets()
	_ = legacy.Set("provider.anthropic", "key-1")
	secrets := LegacySecrets{Current: current, Legacy: legacy}
	value, err := secrets.Get("provider.anthropic")
	if err != nil || value != "key-1" {
		t.Fatalf("got %q, %v", value, err)
	}
	if copied, _ := current.Get("provider.anthropic"); copied != "key-1" {
		t.Fatal("secret was not copied to Rex")
	}
	if err := secrets.Delete("provider.anthropic"); err != nil {
		t.Fatal(err)
	}
	if value, _ := secrets.Get("provider.anthropic"); value != "" {
		t.Fatal("deleted secret came back from Dost's Keychain")
	}
}
