package rex

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreNativePathWithURICharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Rex spaces # percent %.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	reader, err := sql.Open("sqlite", sqliteFileURI(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var count int
	if err := reader.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM bots").Scan(&count); err != nil {
		t.Fatal(err)
	}
}
