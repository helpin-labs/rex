package rex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJobItemsFromFilesAndInline(t *testing.T) {
	dir := t.TempDir()
	bot := Bot{WorkspaceDir: dir}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("schools.csv", "\ufeffid,name,city\nnces-1,Mount Airy High,Mount Airy\nnces-2,\"Whittier High, CA\",Whittier\n")
	write("schools.json", `[{"id":"a","input":{"name":"A"}},{"id":7,"name":"Seven","city":"X"}]`)
	write("schools.jsonl", "{\"id\":\"x\",\"name\":\"X\"}\n\n{\"id\":\"y\",\"name\":\"Y\"}\n")
	write("noid.csv", "name\nA\n")

	items, err := jobItems(bot, startJobInput{ItemsFile: "schools.csv"})
	if err != nil || len(items) != 2 || items[1].ID != "nces-2" {
		t.Fatalf("csv = %+v, %v", items, err)
	}
	var row map[string]string
	_ = json.Unmarshal(items[1].Input, &row)
	if row["name"] != "Whittier High, CA" || row["city"] != "Whittier" {
		t.Fatalf("csv row = %v", row)
	}
	items, err = jobItems(bot, startJobInput{ItemsFile: "schools.json"})
	if err != nil || len(items) != 2 || string(items[0].Input) != `{"name":"A"}` || items[1].ID != "7" {
		t.Fatalf("json = %+v, %v", items, err)
	}
	items, err = jobItems(bot, startJobInput{ItemsFile: "schools.jsonl"})
	if err != nil || len(items) != 2 || items[1].ID != "y" {
		t.Fatalf("jsonl = %+v, %v", items, err)
	}
	if _, err := jobItems(bot, startJobInput{ItemsFile: "noid.csv"}); err == nil || !strings.Contains(err.Error(), "id column") {
		t.Fatalf("csv without id = %v", err)
	}
	if _, err := jobItems(bot, startJobInput{ItemsFile: "../outside.json"}); err == nil {
		t.Fatal("items_file outside the bot's folder was read")
	}
	outside := filepath.Join(t.TempDir(), "secret.json")
	_ = os.WriteFile(outside, []byte(`[{"id":"s"}]`), 0o600)
	if err := os.Symlink(outside, filepath.Join(dir, "link.json")); err == nil {
		if _, err := jobItems(bot, startJobInput{ItemsFile: "link.json"}); err == nil {
			t.Fatal("a symlink out of the bot's folder was followed")
		}
	}
	if _, err := jobItems(bot, startJobInput{}); err == nil {
		t.Fatal("no items accepted")
	}
}

func TestItemLabel(t *testing.T) {
	for input, want := range map[string]string{
		`{"name":"Mount Airy High","city":"Mount Airy","state":"NC"}`: "Mount Airy High, Mount Airy, NC",
		`{"company":"Acme"}`:            "Acme",
		`{"url":"https://example.com"}`: "https://example.com",
		`{"n":1}`:                       "item-1",
		`not json`:                      "item-1",
	} {
		if got := itemLabel("item-1", json.RawMessage(input)); got != want {
			t.Fatalf("itemLabel(%s) = %q, want %q", input, got, want)
		}
	}
}
