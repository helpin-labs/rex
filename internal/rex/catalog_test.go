package rex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hasServer(servers []RunMCPServer, name string) bool {
	for _, server := range servers {
		if server.ServerName == name {
			return true
		}
	}
	return false
}

func TestNormalizeRepo(t *testing.T) {
	for input, want := range map[string]string{"anthropics/skills": "anthropics/skills", "https://github.com/coreyhaines31/marketingskills/tree/main/skills": "coreyhaines31/marketingskills", "github.com/a/b.git": "a/b"} {
		if got, err := NormalizeRepo(input); err != nil || got != want {
			t.Errorf("%q: %q, %v", input, got, err)
		}
	}
	for _, bad := range []string{"", "skills", "https://example.com/a/b c"} {
		if _, err := NormalizeRepo(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

// fakeGitHub serves one repository with two skills, one of them with a script.
func fakeGitHub(t *testing.T) *httptest.Server {
	t.Helper()
	files := map[string]string{
		"skills/pdf/SKILL.md":           "---\nname: pdf\ndescription: Create and fill PDF documents. Use for PDF reports and forms.\nlicense: Apache-2.0\n---\n\n# PDF\n\nUse scripts/fill.py.",
		"skills/pdf/scripts/fill.py":    "print('fill')",
		"skills/cold-email/SKILL.md":    "---\nname: cold-email\ndescription: Write cold outreach emails for sales prospecting.\n---\n\nKeep it short.",
		"skills/cold-email/examples.md": "Example",
		"README.md":                     "readme",
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/acme/skills":
			_, _ = w.Write([]byte(`{"default_branch":"main","stargazers_count":42,"description":"Acme skills","license":{"spdx_id":"MIT"}}`))
		case r.URL.Path == "/repos/acme/skills/commits/main":
			_, _ = w.Write([]byte("abc123"))
		case r.URL.Path == "/repos/acme/skills/git/trees/abc123":
			var tree []map[string]any
			for name, content := range files {
				tree = append(tree, map[string]any{"path": name, "type": "blob", "size": len(content)})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tree": tree})
		case strings.HasPrefix(r.URL.Path, "/raw/acme/skills/abc123/"):
			content, ok := files[strings.TrimPrefix(r.URL.Path, "/raw/acme/skills/abc123/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(content))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestCatalogIndexSearchSuggestInstall(t *testing.T) {
	ctx := context.Background()
	server := fakeGitHub(t)
	defer server.Close()
	host := newSetupTestHost(t)
	host.github = &githubClient{api: server.URL, raw: server.URL + "/raw", client: server.Client()}
	if err := host.Store.EnsureSkillSources(ctx); err != nil {
		t.Fatal(err)
	}
	source, err := host.Store.AddSkillSource(ctx, "https://github.com/acme/skills")
	if err != nil {
		t.Fatal(err)
	}
	if err := host.IndexSource(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	indexed, _ := host.Store.SkillSource(ctx, source.ID)
	if indexed.SkillCount != 2 || indexed.Commit != "abc123" || indexed.Stars != 42 || indexed.IndexedAt == nil {
		t.Fatalf("source = %+v", indexed)
	}

	found, err := host.SearchCatalog(ctx, "write a sales email", 5)
	if err != nil || len(found) == 0 || found[0].Name != "cold-email" {
		t.Fatalf("search = %+v, %v", found, err)
	}
	pdf, _ := host.SearchCatalog(ctx, "pdf report", 5)
	if len(pdf) == 0 || pdf[0].Name != "pdf" || !pdf[0].HasScripts || pdf[0].License != "Apache-2.0" || len(pdf[0].Files) != 2 {
		t.Fatalf("pdf = %+v", pdf)
	}
	// A turned-off source is not searched.
	_ = host.Store.SetSkillSourceEnabled(ctx, source.ID, false)
	if none, _ := host.SearchCatalog(ctx, "pdf", 5); len(none) != 0 {
		t.Fatalf("disabled source searched: %+v", none)
	}
	_ = host.Store.SetSkillSourceEnabled(ctx, source.ID, true)

	bot := &Bot{Name: "Report writer", Instructions: "You write reports."}
	if err := host.SaveBot(ctx, bot); err != nil {
		t.Fatal(err)
	}
	proposal, err := host.ProposeSkill(ctx, bot.ID, pdf[0].ID, "The bot makes PDF reports every week.", ProposalFromRetrospective)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.ProposeSkill(ctx, bot.ID, pdf[0].ID, "again", ProposalFromBot); err == nil {
		t.Fatal("the same skill is suggested once")
	}
	if _, err := host.Skills.Get("pdf"); err == nil {
		t.Fatal("a suggestion installs nothing")
	}
	if err := host.AnswerProposal(ctx, proposal.ID, true); err != nil {
		t.Fatal(err)
	}
	installed, err := host.Skills.Get("pdf")
	if err != nil || installed.Source == nil || installed.Source.Commit != "abc123" || installed.Source.CatalogID != pdf[0].ID {
		t.Fatalf("installed = %+v, %v", installed, err)
	}
	if script, err := os.ReadFile(filepath.Join(host.Skills.Dir, "pdf", "scripts", "fill.py")); err != nil || string(script) != "print('fill')" {
		t.Fatalf("script = %q, %v", script, err)
	}
	if len(installed.Files) != 1 || installed.Files[0] != "scripts/fill.py" {
		t.Fatalf("the origin note is not listed as a skill file: %+v", installed.Files)
	}
	withSkill, _ := host.Store.GetBot(ctx, bot.ID)
	if !containsString(withSkill.Skills, "pdf") {
		t.Fatalf("installing turns the skill on for the bot: %+v", withSkill.Skills)
	}
	if history, _ := host.Store.proposalHistory(ctx, bot.ID, 5); len(history) != 1 || history[0].Outcome != "applied" {
		t.Fatalf("history = %+v", history)
	}
	again, _ := host.SearchCatalog(ctx, "pdf", 5)
	if again[0].Installed != "pdf" {
		t.Fatalf("search shows what is installed: %+v", again[0])
	}
	if _, err := host.ProposeSkill(ctx, bot.ID, pdf[0].ID, "x", ProposalFromBot); err == nil {
		t.Fatal("a bot that has the skill is not offered it again")
	}

	// A local skill with the same name is never overwritten.
	if _, err := host.Skills.Save(Skill{ID: "", Body: "---\nname: cold-email\ndescription: Mine.\n---\n\nMine."}); err != nil {
		t.Fatal(err)
	}
	if _, err := host.InstallCatalogSkill(ctx, found[0].ID, ""); err == nil {
		t.Fatal("installing over the user's own skill is refused")
	}
}

func TestEveryBotCanFindAndSuggestSkills(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	bot := &Bot{Name: "Newsletter", Instructions: "You write a newsletter."}
	if err := host.SaveBot(ctx, bot); err != nil {
		t.Fatal(err)
	}
	servers, _ := host.runMCPServers(ctx, *bot)
	if !hasServer(servers, "catalog") {
		t.Fatalf("a configured bot gets the catalog tools: %+v", servers)
	}
	if prompt := bot.SystemPrompt(nil); !strings.Contains(prompt, "mcp__catalog__suggest_skill") {
		t.Fatal("bots are told how to find and suggest skills")
	}
}
