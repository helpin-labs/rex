package rex

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestResultColumnsSpreadsFields(t *testing.T) {
	columns, cells := resultColumns([]json.RawMessage{
		json.RawMessage(`{"language":"Go","year":2009,"creators":["Griesemer","Pike","Thompson"],"meta":{"a":1}}`),
		nil,
		json.RawMessage(`{"language":"Zig","notes":null,"extra":true}`),
	})
	if got := strings.Join(columns, ","); got != "language,year,creators,meta,notes,extra" {
		t.Fatalf("columns = %s", got)
	}
	if cells[0]["creators"] != "Griesemer; Pike; Thompson" || cells[0]["year"] != "2009" || cells[0]["meta"] != `{"a":1}` {
		t.Fatalf("row 0 = %v", cells[0])
	}
	if len(cells[1]) != 0 || cells[2]["notes"] != "" || cells[2]["extra"] != "true" {
		t.Fatalf("rows = %v", cells)
	}
	columns, cells = resultColumns([]json.RawMessage{json.RawMessage(`{"a":1}`), json.RawMessage(`"plain"`)})
	if len(columns) != 1 || columns[0] != "result" || cells[1]["result"] != `"plain"` {
		t.Fatalf("mixed results = %v %v", columns, cells)
	}
}

func TestJobBrowserNoteForStandardBrowser(t *testing.T) {
	store := openTestStore(t)
	host := &Host{Store: store, Settings: NewSettingsManager(store, &MemorySecrets{})}
	ctx := context.Background()
	for _, tc := range []struct {
		backend string
		browser bool
		want    bool
	}{{"standard", true, true}, {"extension", true, false}, {"standard", false, false}} {
		bot := Bot{ID: "bot-" + tc.backend + fmt.Sprint(tc.browser), Name: "Bot", BrowserBackend: tc.backend, Capabilities: Capabilities{Browser: tc.browser}}
		if err := host.Store.SaveBot(ctx, &bot); err != nil {
			t.Fatal(err)
		}
		if got := host.jobBrowserNote(ctx, bot.ID) != ""; got != tc.want {
			t.Errorf("%s browser=%v: note shown = %v, want %v", tc.backend, tc.browser, got, tc.want)
		}
	}
}
