package rex

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Rex was called Dost. These are the names it used on the Mac; on the first
// launch of Rex, MigrateFromDost moves what they hold to Rex's names.
const (
	LegacyDataDirName     = "ai.dost.desktop"
	LegacyKeychainService = "ai.dost.desktop"
	legacyDBName          = "dost.db"
	legacyLogName         = "dost.log"
	legacyWorkspaceName   = "Dost"
	migratedMarker        = "MOVED-TO-REX.txt"
)

// MigrateFromDost moves Dost's data to Rex on Rex's first launch: the app
// data folder (database, memories, browser profiles), the workspace folder
// (~/Dost to ~/Rex) and the paths stored in the database. It does nothing
// when there is no Dost data or Rex already has a database. Keychain items
// move lazily through LegacySecrets.
func MigrateFromDost(dataDir, workspaceRoot string) error {
	oldDir := filepath.Join(filepath.Dir(dataDir), LegacyDataDirName)
	oldDB := filepath.Join(oldDir, legacyDBName)
	newDB := filepath.Join(dataDir, "rex.db")
	if _, err := os.Stat(oldDB); err != nil {
		return nil
	}
	if _, err := os.Stat(newDB); err == nil {
		return nil
	}
	if running(filepath.Join(oldDir, "connection.json")) {
		return errors.New("Dost is still running. Quit Dost, then open Rex again")
	}
	slog.Info("moving Dost data to Rex", "from", oldDir, "to", dataDir)

	// A copy of the database as it was, kept in the old folder.
	backup := filepath.Join(oldDir, legacyDBName+"."+time.Now().Format("20060102-150405")+".before-rex.bak")
	if err := vacuumInto(oldDB, backup); err != nil {
		return fmt.Errorf("back up Dost database: %w", err)
	}

	// Move each entry; the desktop shell may already have created the new
	// folder, so it is filled rather than renamed.
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(oldDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".before-rex.bak") || name == "connection.json" || name == migratedMarker {
			continue
		}
		target := name
		switch {
		case name == legacyDBName:
			target = "rex.db"
		case strings.HasPrefix(name, legacyDBName+"-"): // -wal, -shm
			target = "rex.db" + strings.TrimPrefix(name, legacyDBName)
		case name == legacyLogName:
			target = "rex.log"
		}
		destination := filepath.Join(dataDir, target)
		if _, err := os.Stat(destination); err == nil {
			continue
		}
		if err := os.Rename(filepath.Join(oldDir, name), destination); err != nil {
			return fmt.Errorf("move %s: %w", name, err)
		}
	}

	// The workspace: ~/Dost becomes ~/Rex unless ~/Rex is already in use.
	home, _ := os.UserHomeDir()
	oldWorkspace := filepath.Join(home, legacyWorkspaceName)
	if home != "" && filepath.Clean(workspaceRoot) == filepath.Join(home, "Rex") {
		if _, err := os.Stat(oldWorkspace); err == nil {
			if _, err := os.Stat(workspaceRoot); errors.Is(err, os.ErrNotExist) {
				if err := os.Rename(oldWorkspace, workspaceRoot); err != nil {
					return fmt.Errorf("move %s: %w", oldWorkspace, err)
				}
			}
		}
	}
	if err := rewriteLegacyRows(newDB, oldDir, dataDir, oldWorkspace, workspaceRoot); err != nil {
		return err
	}
	note := "Dost is now Rex. Its data moved to " + dataDir + " on " + time.Now().Format(time.RFC1123) + ".\nThe database as it was is kept here as " + filepath.Base(backup) + ".\n"
	_ = os.WriteFile(filepath.Join(oldDir, migratedMarker), []byte(note), 0o600)
	slog.Info("moved Dost data to Rex")
	return nil
}

// rewriteLegacyRows points stored paths at the new folders and renames the
// main assistant, unless the user had renamed it.
func rewriteLegacyRows(dbPath, oldDir, newDir, oldWorkspace, newWorkspace string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	type move struct{ from, to string }
	for _, m := range []move{{oldDir, newDir}, {oldWorkspace, newWorkspace}} {
		if m.from == m.to {
			continue
		}
		// Only whole path prefixes: "/Users/a/Dost" or "/Users/a/Dost/…".
		if _, err := tx.Exec(`UPDATE bots SET workspace_dir = ? || substr(workspace_dir, length(?) + 1) WHERE workspace_dir = ? OR workspace_dir LIKE ? ESCAPE '\'`,
			m.to, m.from, m.from, likePrefix(m.from)); err != nil {
			return fmt.Errorf("rewrite bot folders: %w", err)
		}
	}
	if _, err := tx.Exec(`UPDATE bots SET name = 'Rex', instructions = replace(instructions, 'Dost', 'Rex') WHERE role = 'main' AND name = 'Dost'`); err != nil {
		return fmt.Errorf("rename main assistant: %w", err)
	}
	if _, err := tx.Exec(`UPDATE turns SET origin = 'rex' WHERE origin = 'dost'`); err != nil {
		return fmt.Errorf("update message origins: %w", err)
	}
	return tx.Commit()
}

func likePrefix(path string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(path)
	return escaped + string(filepath.Separator) + "%"
}

func vacuumInto(dbPath, backup string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`VACUUM INTO ?`, backup)
	return err
}

// running reports whether the Dost host that wrote connection.json still
// answers on its address.
func running(connectionFile string) bool {
	data, err := os.ReadFile(connectionFile)
	if err != nil {
		return false
	}
	var url string
	if _, after, ok := strings.Cut(string(data), `"url":"`); ok {
		url, _, _ = strings.Cut(after, `"`)
	}
	if url == "" {
		return false
	}
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(url + "/healthz")
	if err != nil {
		return false
	}
	response.Body.Close()
	return true
}

// LegacySecrets reads a secret from Dost's Keychain service when Rex does
// not have it yet, and copies it over, so provider keys, bot logins and MCP
// sign-ins carry over without listing the Keychain. Dost's items are left in
// place; deleting a secret deletes both.
type LegacySecrets struct {
	Current Secrets
	Legacy  Secrets
}

func (s LegacySecrets) Get(key string) (string, error) {
	value, err := s.Current.Get(key)
	if err != nil || value != "" {
		return value, err
	}
	old, err := s.Legacy.Get(key)
	if err != nil || old == "" {
		return "", nil
	}
	if err := s.Current.Set(key, old); err != nil {
		slog.Warn("could not copy a Dost Keychain item to Rex", "key", key, "error", err)
	}
	return old, nil
}

func (s LegacySecrets) Set(key, value string) error { return s.Current.Set(key, value) }

func (s LegacySecrets) Delete(key string) error {
	_ = s.Legacy.Delete(key)
	return s.Current.Delete(key)
}
