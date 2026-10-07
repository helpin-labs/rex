package rex

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for name, content := range files {
		writer, _ := archive.Create(name)
		_, _ = writer.Write([]byte(content))
	}
	_ = archive.Close()
	return buffer.Bytes()
}

func TestOldOneFileSkillsBecomeFolders(t *testing.T) {
	library := SkillLibrary{Dir: t.TempDir()}
	// A pasted SKILL.md that Rex stored under its own title heading.
	_ = os.WriteFile(filepath.Join(library.Dir, "publish-blog.md"), []byte("# Publish Blog\n\n---\nname: publish-blog\ndescription: Publish a page to the hub.\n---\n\n# Publish\n\nAsk for the key.\n"), 0o644)
	_ = os.WriteFile(filepath.Join(library.Dir, "notes.md"), []byte("# Meeting notes\n\nSummarise meetings.\n"), 0o644)
	if err := library.EnsureDefaults(); err != nil {
		t.Fatal(err)
	}
	skills, _ := library.List()
	if len(skills) != 2 {
		t.Fatalf("both skills are kept: %+v", skills)
	}
	blog, _ := library.Get("publish-blog")
	if !strings.HasPrefix(blog.Body, "---\nname: publish-blog") || blog.Description != "Publish a page to the hub." {
		t.Fatalf("the front matter comes first again: %q", blog.Body)
	}
	notes, _ := library.Get("notes")
	if notes.Description != "Meeting notes" || !strings.Contains(notes.Body, "Summarise meetings.") {
		t.Fatalf("a plain skill gets front matter: %+v", notes)
	}
	if _, err := os.Stat(filepath.Join(library.Dir, "notes.md")); !os.IsNotExist(err) {
		t.Fatal("the old file is gone")
	}
}

func TestSkillsNeedADescription(t *testing.T) {
	library := SkillLibrary{Dir: t.TempDir()}
	if _, err := library.Save(Skill{Body: "---\nname: x\n---\n\nDo it."}); err == nil {
		t.Fatal("front matter without a description is refused")
	}
	skill, err := library.Save(Skill{Title: "Weekly report", Body: "Write the weekly report."})
	if err != nil || skill.ID != "weekly-report" || skill.Description != "Weekly report" {
		t.Fatalf("plain text gets front matter: %+v %v", skill, err)
	}
}

func TestImportPackagesAndServesMultiFileSkills(t *testing.T) {
	host := newSetupTestHost(t)
	archive := zipOf(t, map[string]string{
		"publish-blog/SKILL.md":            "---\nname: publish-blog\ndescription: Publish a page.\n---\n\n# Publish\n\nRun scripts/publish.py.\n",
		"publish-blog/scripts/publish.py":  "print('publish')\n",
		"publish-blog/references/api.md":   "# API\n",
		"__MACOSX/publish-blog/._SKILL.md": "junk",
	})
	skill, err := host.Skills.Import(archive)
	if err != nil {
		t.Fatal(err)
	}
	if skill.ID != "publish-blog" || strings.Join(skill.Files, ",") != "references/api.md,scripts/publish.py" {
		t.Fatalf("the folder and its files are imported: %+v", skill)
	}
	for name, files := range map[string]map[string]string{
		"two skills":     {"a/SKILL.md": "---\nname: a\ndescription: a\n---\nA", "b/SKILL.md": "---\nname: b\ndescription: b\n---\nB"},
		"escape":         {"x/SKILL.md": "---\nname: x\ndescription: x\n---\nX", "x/../../evil.sh": "rm -rf"},
		"no frontmatter": {"y/SKILL.md": "just text"},
	} {
		if _, err := host.Skills.Import(zipOf(t, files)); err == nil {
			t.Fatalf("%s is refused", name)
		}
	}

	// The runtime looks the skill up and downloads its folder.
	request := httptest.NewRequest(http.MethodPost, "/runtime/skills/by-id", strings.NewReader(`{"skill_id":"publish-blog"}`))
	recorder := httptest.NewRecorder()
	host.lookupSkill(recorder, request)
	var found runtimeSkill
	_ = json.Unmarshal(recorder.Body.Bytes(), &found)
	if found.Key != "publish-blog" || found.VersionKey != skill.Version || strings.Contains(found.Instructions, "name: publish-blog") || !strings.Contains(found.Instructions, "Run scripts/publish.py") {
		t.Fatalf("lookup returns the instructions without front matter: %+v", found)
	}
	packaged, err := host.Skills.Package("publish-blog")
	if err != nil {
		t.Fatal(err)
	}
	reader, _ := zip.NewReader(bytes.NewReader(packaged), int64(len(packaged)))
	var names []string
	for _, file := range reader.File {
		names = append(names, file.Name)
	}
	if strings.Join(names, ",") != "publish-blog/SKILL.md,publish-blog/references/api.md,publish-blog/scripts/publish.py" {
		t.Fatalf("the package holds the folder under one root: %v", names)
	}
	bot := Bot{Name: "Data", Skills: []string{"publish-blog"}}
	if refs := bot.RuntimeAgent("").Skills; len(refs) != 1 || refs[0].Key != "publish-blog" {
		t.Fatalf("the bot's runtime agent references its skills: %+v", refs)
	}
}
