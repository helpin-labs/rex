package rex

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The skill catalog: GitHub repositories of Agent Skills that Rex can search
// and install from. Rex indexes each enabled source at a pinned commit (the
// file list and each SKILL.md's name and description, no code), bots and
// retrospectives search the index and suggest a skill, and only the user
// installs one. An installed skill is copied into ~/Rex/skills with a
// .rex-source.json that says where it came from.

type SkillSource struct {
	ID          string     `json:"id"`
	Repo        string     `json:"repo"` // owner/name
	Enabled     bool       `json:"enabled"`
	Builtin     bool       `json:"builtin"`
	Note        string     `json:"note"`
	Stars       int        `json:"stars"`
	License     string     `json:"license"`
	Commit      string     `json:"commit"`
	SkillCount  int        `json:"skill_count"`
	IndexedAt   *time.Time `json:"indexed_at,omitempty"`
	Error       string     `json:"error,omitempty"`
	Description string     `json:"description"`
}

type CatalogSkill struct {
	ID          string   `json:"id"` // source id + ":" + folder
	SourceID    string   `json:"source_id"`
	Repo        string   `json:"repo"`
	Dir         string   `json:"dir"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	License     string   `json:"license,omitempty"`
	HasScripts  bool     `json:"has_scripts"`
	Files       []string `json:"files"`
	Commit      string   `json:"commit"`
	URL         string   `json:"url"`
	Installed   string   `json:"installed,omitempty"` // local skill id when installed
}

// SkillOrigin is kept in an installed skill's folder.
type SkillOrigin struct {
	Repo        string    `json:"repo"`
	Commit      string    `json:"commit"`
	Dir         string    `json:"dir"`
	CatalogID   string    `json:"catalog_id"`
	InstalledAt time.Time `json:"installed_at"`
}

const skillOriginFile = ".rex-source.json"

func readSkillOrigin(dir string) *SkillOrigin {
	raw, err := os.ReadFile(filepath.Join(dir, skillOriginFile))
	if err != nil {
		return nil
	}
	var origin SkillOrigin
	if json.Unmarshal(raw, &origin) != nil || origin.Repo == "" {
		return nil
	}
	return &origin
}

// builtinSkillSources are curated, popular collections. Rex's bots are
// personal assistants, so Anthropic's general set and the most used
// marketing set are on by default; sales and coding are one switch away.
var builtinSkillSources = []struct {
	Repo, Note string
	Enabled    bool
}{
	{"anthropics/skills", "Anthropic's official skills: PDF, Word, Excel, PowerPoint, design and writing", true},
	{"coreyhaines31/marketingskills", "Marketing: copywriting, SEO, conversion, analytics, launches and growth", true},
	{"kostja94/marketing-skills", "Marketing: SEO, social media, influencers and content, 170+ skills", false},
	{"zubair-trabzada/ai-sales-team-claude", "Sales: prospect research, lead qualification, outreach sequences, call prep", false},
	{"louisblythe/Sales-Skills", "Sales playbooks for B2B: prospecting, discovery, objections, closing", false},
	{"obra/superpowers", "Working habits for coding agents: planning, debugging, test-first", false},
	{"mattpocock/skills", "TypeScript and coding workflows", false},
	{"wshobson/agents", "Development and operations", false},
	{"vercel-labs/agent-skills", "Web and front-end development", false},
	{"openai/skills", "OpenAI's skill set", false},
}

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// NormalizeRepo accepts "owner/name" or a github.com URL.
func NormalizeRepo(value string) (string, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(strings.TrimSuffix(value, "/"), ".git")
	for _, prefix := range []string{"https://github.com/", "http://github.com/", "github.com/"} {
		value = strings.TrimPrefix(value, prefix)
	}
	if parts := strings.Split(value, "/"); len(parts) > 2 {
		value = parts[0] + "/" + parts[1]
	}
	if !repoPattern.MatchString(value) {
		return "", errors.New("give a GitHub repository as owner/name or its github.com link")
	}
	return value, nil
}

func sourceID(repo string) string { return Slug(strings.ReplaceAll(repo, "/", "-")) }

// --- store ---

const sourceColumns = `id,repo,enabled,builtin,note,stars,license,commit_sha,skill_count,indexed_at,error,description`

func scanSource(row interface{ Scan(...any) error }) (*SkillSource, error) {
	var item SkillSource
	err := row.Scan(&item.ID, &item.Repo, &item.Enabled, &item.Builtin, &item.Note, &item.Stars, &item.License, &item.Commit, &item.SkillCount, &item.IndexedAt, &item.Error, &item.Description)
	return &item, err
}

func (s *Store) SkillSources(ctx context.Context) ([]SkillSource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sourceColumns+` FROM skill_sources ORDER BY builtin DESC, stars DESC, repo`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SkillSource{}
	for rows.Next() {
		item, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (s *Store) SkillSource(ctx context.Context, id string) (*SkillSource, error) {
	item, err := scanSource(s.db.QueryRowContext(ctx, `SELECT `+sourceColumns+` FROM skill_sources WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return item, err
}

// EnsureSkillSources adds the built-in sources a database does not have yet.
// A source the user removed is not added back: removal keeps a disabled row.
func (s *Store) EnsureSkillSources(ctx context.Context) error {
	for _, source := range builtinSkillSources {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO skill_sources(id,repo,enabled,builtin,note) VALUES(?,?,?,1,?) ON CONFLICT(id) DO NOTHING`,
			sourceID(source.Repo), source.Repo, source.Enabled, source.Note); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) AddSkillSource(ctx context.Context, repo string) (*SkillSource, error) {
	repo, err := NormalizeRepo(repo)
	if err != nil {
		return nil, err
	}
	id := sourceID(repo)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO skill_sources(id,repo,enabled,builtin,note) VALUES(?,?,1,0,'') ON CONFLICT(id) DO UPDATE SET enabled=1`, id, repo); err != nil {
		return nil, err
	}
	return s.SkillSource(ctx, id)
}

func (s *Store) SetSkillSourceEnabled(ctx context.Context, id string, enabled bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE skill_sources SET enabled=? WHERE id=?`, enabled, id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RemoveSkillSource(ctx context.Context, id string) error {
	return withTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_skills WHERE source_id=?`, id); err != nil {
			return err
		}
		// A built-in source stays known but off, so it is not added back.
		if _, err := tx.ExecContext(ctx, `UPDATE skill_sources SET enabled=0, skill_count=0, indexed_at=NULL WHERE id=? AND builtin=1`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM skill_sources WHERE id=? AND builtin=0`, id)
		return err
	})
}

func (s *Store) saveIndex(ctx context.Context, source SkillSource, skills []CatalogSkill) error {
	now := time.Now().UTC()
	return withTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_skills WHERE source_id=?`, source.ID); err != nil {
			return err
		}
		for _, skill := range skills {
			files, _ := json.Marshal(skill.Files)
			if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_skills(id,source_id,dir,name,description,license,has_scripts,files,commit_sha) VALUES(?,?,?,?,?,?,?,?,?)`,
				skill.ID, source.ID, skill.Dir, skill.Name, skill.Description, skill.License, skill.HasScripts, string(files), source.Commit); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE skill_sources SET stars=?, license=?, commit_sha=?, skill_count=?, indexed_at=?, error='', description=? WHERE id=?`,
			source.Stars, source.License, source.Commit, len(skills), now, source.Description, source.ID)
		return err
	})
}

func (s *Store) setSourceError(ctx context.Context, id, message string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE skill_sources SET error=? WHERE id=?`, message, id)
}

const catalogColumns = `c.id,c.source_id,s.repo,c.dir,c.name,c.description,c.license,c.has_scripts,c.files,c.commit_sha`

func scanCatalogSkill(row interface{ Scan(...any) error }) (*CatalogSkill, error) {
	var item CatalogSkill
	var files string
	if err := row.Scan(&item.ID, &item.SourceID, &item.Repo, &item.Dir, &item.Name, &item.Description, &item.License, &item.HasScripts, &files, &item.Commit); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(files), &item.Files)
	item.URL = fmt.Sprintf("https://github.com/%s/tree/%s/%s", item.Repo, item.Commit, item.Dir)
	return &item, nil
}

func (s *Store) CatalogSkill(ctx context.Context, id string) (*CatalogSkill, error) {
	item, err := scanCatalogSkill(s.db.QueryRowContext(ctx, `SELECT `+catalogColumns+` FROM catalog_skills c JOIN skill_sources s ON s.id=c.source_id WHERE c.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return item, err
}

func (s *Store) enabledCatalog(ctx context.Context) ([]CatalogSkill, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+catalogColumns+` FROM catalog_skills c JOIN skill_sources s ON s.id=c.source_id WHERE s.enabled=1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []CatalogSkill
	for rows.Next() {
		item, err := scanCatalogSkill(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

// --- search ---

var wordPattern = regexp.MustCompile(`[a-z0-9]+`)

var stopWords = map[string]bool{"a": true, "an": true, "the": true, "and": true, "or": true, "to": true, "for": true, "of": true, "in": true, "on": true, "with": true, "use": true, "when": true, "user": true, "skill": true, "skills": true, "is": true, "it": true, "how": true, "that": true, "this": true, "from": true, "by": true, "my": true, "me": true, "i": true}

func searchWords(text string) []string {
	var words []string
	for _, word := range wordPattern.FindAllString(strings.ToLower(text), -1) {
		if !stopWords[word] {
			words = append(words, word)
		}
	}
	return words
}

// SearchCatalog ranks enabled sources' skills by how well their name and
// description match the query. An empty query lists them all.
func (h *Host) SearchCatalog(ctx context.Context, query string, limit int) ([]CatalogSkill, error) {
	items, err := h.Store.enabledCatalog(ctx)
	if err != nil {
		return nil, err
	}
	installed := h.installedOrigins()
	terms := searchWords(query)
	type scored struct {
		item  CatalogSkill
		score int
	}
	var results []scored
	for _, item := range items {
		item.Installed = installed[item.ID]
		if len(terms) == 0 {
			results = append(results, scored{item, 0})
			continue
		}
		name := strings.ToLower(item.Name + " " + strings.ReplaceAll(item.Dir, "/", " "))
		description := strings.ToLower(item.Description)
		score := 0
		for _, term := range terms {
			switch {
			case strings.Contains(name, term):
				score += 5
			case len(term) > 3 && strings.Contains(name, term[:len(term)-1]):
				score += 3
			}
			if strings.Contains(description, term) {
				score += 2
			}
		}
		if score > 0 {
			results = append(results, scored{item, score})
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return results[i].item.Name < results[j].item.Name
	})
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	out := []CatalogSkill{}
	for index, result := range results {
		if index >= limit {
			break
		}
		out = append(out, result.item)
	}
	return out, nil
}

// installedOrigins maps catalog ids to the local skills installed from them.
func (h *Host) installedOrigins() map[string]string {
	out := map[string]string{}
	skills, err := h.Skills.List()
	if err != nil {
		return out
	}
	for _, skill := range skills {
		if skill.Source != nil && skill.Source.CatalogID != "" {
			out[skill.Source.CatalogID] = skill.ID
		}
	}
	return out
}

// --- GitHub ---

// githubClient reads public repositories. GitHub allows 60 anonymous API
// requests an hour; indexing a source costs three, and SKILL.md files come
// from raw.githubusercontent.com, which is not counted.
type githubClient struct {
	api, raw string
	client   *http.Client
}

func newGitHubClient() *githubClient {
	return &githubClient{api: "https://api.github.com", raw: "https://raw.githubusercontent.com", client: &http.Client{Timeout: 30 * time.Second}}
}

func (g *githubClient) get(ctx context.Context, rawURL string, accept string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "Rex-desktop")
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	response, err := g.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s is too large", rawURL)
	}
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests {
		return nil, errors.New("GitHub's hourly limit for anonymous requests was reached; try again later")
	}
	if response.StatusCode == http.StatusNotFound {
		return nil, errors.New("not found on GitHub (is the repository public?)")
	}
	if response.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub answered %s", response.Status)
	}
	return body, nil
}

type githubTreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

var scriptPattern = regexp.MustCompile(`(?i)\.(py|sh|bash|zsh|js|mjs|cjs|ts|rb|pl|php|ps1|bat|exe|bin)$`)

const (
	maxCatalogSkillsPerSource = 600
	catalogRefresh            = 7 * 24 * time.Hour
)

// IndexSource reads a repository at its current commit and records its skills.
func (h *Host) IndexSource(ctx context.Context, id string) error {
	source, err := h.Store.SkillSource(ctx, id)
	if err != nil {
		return err
	}
	if err := h.indexSource(ctx, source); err != nil {
		h.Store.setSourceError(ctx, id, err.Error())
		return err
	}
	return nil
}

func (h *Host) indexSource(ctx context.Context, source *SkillSource) error {
	g := h.github
	repoPath := url.PathEscape(strings.Split(source.Repo, "/")[0]) + "/" + url.PathEscape(strings.Split(source.Repo, "/")[1])
	raw, err := g.get(ctx, g.api+"/repos/"+repoPath, "application/vnd.github+json", 1<<20)
	if err != nil {
		return err
	}
	var repo struct {
		DefaultBranch string `json:"default_branch"`
		Stars         int    `json:"stargazers_count"`
		Description   string `json:"description"`
		License       *struct {
			SPDX string `json:"spdx_id"`
		} `json:"license"`
	}
	if err := json.Unmarshal(raw, &repo); err != nil || repo.DefaultBranch == "" {
		return errors.New("could not read the repository")
	}
	commit, err := g.get(ctx, g.api+"/repos/"+repoPath+"/commits/"+url.PathEscape(repo.DefaultBranch), "application/vnd.github.sha", 1<<10)
	if err != nil {
		return err
	}
	source.Commit = strings.TrimSpace(string(commit))
	source.Stars, source.Description = repo.Stars, repo.Description
	if repo.License != nil && repo.License.SPDX != "NOASSERTION" {
		source.License = repo.License.SPDX
	}
	raw, err = g.get(ctx, g.api+"/repos/"+repoPath+"/git/trees/"+source.Commit+"?recursive=1", "application/vnd.github+json", 64<<20)
	if err != nil {
		return err
	}
	var tree struct {
		Tree      []githubTreeEntry `json:"tree"`
		Truncated bool              `json:"truncated"`
	}
	if err := json.Unmarshal(raw, &tree); err != nil {
		return errors.New("could not read the repository's files")
	}
	skills := catalogFromTree(source, tree.Tree)
	if len(skills) > maxCatalogSkillsPerSource {
		skills = skills[:maxCatalogSkillsPerSource]
	}
	// Read each SKILL.md for its name and description, a few at a time.
	var wg sync.WaitGroup
	limit := make(chan struct{}, 8)
	for index := range skills {
		wg.Add(1)
		go func(skill *CatalogSkill) {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			content, err := g.get(ctx, g.raw+"/"+source.Repo+"/"+source.Commit+"/"+escapePath(path.Join(skill.Dir, skillFile)), "", 256<<10)
			if err != nil {
				return
			}
			front, _, ok := splitFrontMatter(string(content))
			if !ok {
				return
			}
			skill.Name = strings.TrimSpace(front.Name)
			skill.Description = strings.TrimSpace(front.Description)
			skill.License = strings.TrimSpace(front.License)
		}(&skills[index])
	}
	wg.Wait()
	kept := skills[:0]
	for _, skill := range skills {
		if skill.Description == "" {
			continue
		}
		if skill.Name == "" {
			skill.Name = path.Base(skill.Dir)
		}
		if skill.License == "" {
			skill.License = source.License
		}
		kept = append(kept, skill)
	}
	if len(kept) == 0 && tree.Truncated {
		return errors.New("this repository is too large to index")
	}
	slog.Info("indexed skill source", "repo", source.Repo, "commit", source.Commit, "skills", len(kept))
	return h.Store.saveIndex(ctx, *source, kept)
}

// catalogFromTree finds skill folders (those with a SKILL.md) and their files.
func catalogFromTree(source *SkillSource, entries []githubTreeEntry) []CatalogSkill {
	var dirs []string
	for _, entry := range entries {
		if entry.Type == "blob" && path.Base(entry.Path) == skillFile && !strings.Contains(entry.Path, "/.") && !strings.HasPrefix(entry.Path, ".") {
			dirs = append(dirs, path.Dir(entry.Path))
		}
	}
	sort.Strings(dirs)
	var skills []CatalogSkill
	for _, dir := range dirs {
		if dir == "." {
			continue // a repository that is itself one skill is not supported yet
		}
		skill := CatalogSkill{ID: source.ID + ":" + dir, SourceID: source.ID, Repo: source.Repo, Dir: dir, Commit: source.Commit}
		var total int64
		for _, entry := range entries {
			if entry.Type != "blob" || !strings.HasPrefix(entry.Path, dir+"/") {
				continue
			}
			relative := strings.TrimPrefix(entry.Path, dir+"/")
			if strings.HasPrefix(relative, ".") || strings.Contains(relative, "/.") {
				continue
			}
			// A nested skill belongs to its own folder.
			if nested := path.Dir(relative); nested != "." && containsSkill(dirs, path.Join(dir, nested), dir) {
				continue
			}
			skill.Files = append(skill.Files, relative)
			total += entry.Size
			if scriptPattern.MatchString(relative) {
				skill.HasScripts = true
			}
		}
		if len(skill.Files) == 0 || len(skill.Files) > maxSkillFiles || total > maxSkillBytes {
			continue
		}
		skills = append(skills, skill)
	}
	return skills
}

func containsSkill(dirs []string, folder, parent string) bool {
	for _, dir := range dirs {
		if dir != parent && (folder == dir || strings.HasPrefix(folder, dir+"/")) {
			return true
		}
	}
	return false
}

func escapePath(value string) string {
	parts := strings.Split(value, "/")
	for index, part := range parts {
		parts[index] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// CatalogPreview returns a catalog skill's SKILL.md at its pinned commit.
func (h *Host) CatalogPreview(ctx context.Context, id string) (string, error) {
	item, err := h.Store.CatalogSkill(ctx, id)
	if err != nil {
		return "", err
	}
	content, err := h.github.get(ctx, h.github.raw+"/"+item.Repo+"/"+item.Commit+"/"+escapePath(path.Join(item.Dir, skillFile)), "", 256<<10)
	return string(content), err
}

// InstallCatalogSkill copies a catalog skill at its pinned commit into the
// skill library, and turns it on for a bot when one is given.
func (h *Host) InstallCatalogSkill(ctx context.Context, id, botID string) (*Skill, error) {
	item, err := h.Store.CatalogSkill(ctx, id)
	if err != nil {
		return nil, err
	}
	localID := Slug(item.Name)
	if existing, err := h.Skills.Get(localID); err == nil && (existing.Source == nil || existing.Source.CatalogID != item.ID) {
		return nil, fmt.Errorf("you already have a skill called %q; rename or delete it first", localID)
	}
	contents := map[string][]byte{}
	var total int
	for _, file := range item.Files {
		clean := path.Clean(file)
		if clean == "." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) || strings.Contains(clean, "\\") {
			return nil, fmt.Errorf("unsafe path in the skill: %s", file)
		}
		content, err := h.github.get(ctx, h.github.raw+"/"+item.Repo+"/"+item.Commit+"/"+escapePath(path.Join(item.Dir, clean)), "", maxSkillBytes)
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", clean, err)
		}
		total += len(content)
		if total > maxSkillBytes {
			return nil, fmt.Errorf("the skill is larger than %d MB", maxSkillBytes>>20)
		}
		contents[clean] = content
	}
	if _, ok := contents[skillFile]; !ok {
		return nil, errors.New("the skill has no SKILL.md")
	}
	origin, _ := json.MarshalIndent(SkillOrigin{Repo: item.Repo, Commit: item.Commit, Dir: item.Dir, CatalogID: item.ID, InstalledAt: time.Now().UTC()}, "", "  ")
	contents[skillOriginFile] = origin
	skill, err := h.Skills.writeFolder(localID, contents)
	if err != nil {
		return nil, err
	}
	slog.Info("installed catalog skill", "skill", skill.ID, "repo", item.Repo, "commit", item.Commit)
	if botID != "" {
		bot, err := h.Store.GetBot(ctx, botID)
		if err != nil {
			return skill, err
		}
		if !containsString(bot.Skills, skill.ID) {
			bot.Skills = append(bot.Skills, skill.ID)
			if err := h.SaveBot(ctx, bot); err != nil {
				return skill, err
			}
		}
	}
	return skill, nil
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

// CatalogWorker indexes enabled sources that were never indexed or are a
// week old, shortly after start and then daily.
func (h *Host) CatalogWorker(ctx context.Context) {
	if err := h.Store.EnsureSkillSources(ctx); err != nil {
		slog.Warn("skill sources", "error", err)
	}
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		sources, _ := h.Store.SkillSources(ctx)
		for _, source := range sources {
			if !source.Enabled || (source.IndexedAt != nil && time.Since(*source.IndexedAt) < catalogRefresh) {
				continue
			}
			if err := h.IndexSource(ctx, source.ID); err != nil {
				slog.Warn("index skill source", "repo", source.Repo, "error", err)
			}
		}
		timer.Reset(24 * time.Hour)
	}
}

// --- tools for bots ---

// CatalogToken is the bearer token for one bot's catalog tools.
func (h *Host) CatalogToken(botID string) string {
	mac := hmac.New(sha256.New, h.cfg.MCPSecret)
	mac.Write([]byte("catalog:" + botID))
	return hex.EncodeToString(mac.Sum(nil))
}

// catalogMCPServer offers find_skills_in_catalog, and suggest_skill unless
// suggestions go through another tool (retrospectives).
func (h *Host) catalogMCPServer(botID string, suggest bool) RunMCPServer {
	tools := []RunMCPTool{{Name: "find_skills_in_catalog", Access: "read"}}
	if suggest {
		// Suggesting installs nothing; the user installs it in the app.
		tools = append(tools, RunMCPTool{Name: "suggest_skill", Access: "read"})
	}
	return RunMCPServer{
		ServerID: "rex-catalog", ServerName: "catalog", Transport: "streamable_http",
		URL: h.cfg.HostURL + "/mcp/catalog/" + botID, Tools: tools,
		Credential: &RunMCPCredential{Type: "bearer_token", AccessToken: h.CatalogToken(botID)},
	}
}

func (h *Host) catalogMCP() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		botID := strings.TrimPrefix(r.URL.Path, "/mcp/catalog/")
		if botID == "" || !bearerMatches(r, h.CatalogToken(botID)) {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
			return
		}
		server := protocol.NewServer(&protocol.Implementation{Name: "rex-catalog", Version: "1.0.0"}, nil)
		server.AddTool(&protocol.Tool{
			Name:        "find_skills_in_catalog",
			Description: "Search the skill catalog: curated GitHub collections of Agent Skills the user has turned on (for example documents, marketing, sales). Use plain words for the job, like \"pdf report\" or \"cold email\". Results are suggestions only; installing needs the user.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"query"}, "properties": map[string]any{
				"query": map[string]any{"type": "string", "minLength": 2, "maxLength": 200},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 15},
			}},
		}, func(ctx context.Context, request *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			var input struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
				return toolError(err), nil
			}
			if input.Limit == 0 {
				input.Limit = 8
			}
			found, err := h.SearchCatalog(ctx, input.Query, input.Limit)
			if err != nil {
				return toolError(err), nil
			}
			results := []map[string]any{}
			for _, item := range found {
				result := map[string]any{"catalog_id": item.ID, "name": item.Name, "description": item.Description, "source": item.Repo, "has_scripts": item.HasScripts}
				if item.License != "" {
					result["license"] = item.License
				}
				if item.Installed != "" {
					result["installed_as"] = item.Installed
				}
				results = append(results, result)
			}
			message := "Suggest a skill only when it clearly fits the job."
			if len(results) == 0 {
				message = "Nothing matched. Try other words, or go without a skill."
			}
			return toolResult(map[string]any{"results": results, "message": message}), nil
		})
		server.AddTool(&protocol.Tool{
			Name:        "suggest_skill",
			Description: "Suggest installing a skill from the catalog (a catalog_id from find_skills_in_catalog). The user sees the skill, its source and files and decides; nothing is installed until they do. Rex can name the bot it is for.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"catalog_id", "reason"}, "properties": map[string]any{
				"catalog_id": map[string]any{"type": "string"},
				"reason":     map[string]any{"type": "string", "minLength": 5, "maxLength": 400, "description": "One or two sentences for the user: why this skill helps with their job."},
				"bot":        map[string]any{"type": "string", "description": "Rex only: the name of the bot that should get the skill. Leave empty for yourself."},
			}},
		}, func(ctx context.Context, request *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			var input struct {
				CatalogID string `json:"catalog_id"`
				Reason    string `json:"reason"`
				Bot       string `json:"bot"`
			}
			if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
				return toolError(err), nil
			}
			target := botID
			if name := strings.TrimSpace(input.Bot); name != "" {
				caller, err := h.Store.GetBot(ctx, botID)
				if err != nil || caller.Role != RoleMain {
					return toolError(errors.New("only Rex can suggest skills for other bots")), nil
				}
				other, err := h.botByName(ctx, name)
				if err != nil {
					return toolError(err), nil
				}
				target = other.ID
			}
			proposal, err := h.ProposeSkill(ctx, target, input.CatalogID, input.Reason, ProposalFromBot)
			if err != nil {
				return toolError(err), nil
			}
			return toolResult(map[string]any{"status": "suggested", "summary": proposal.Summary, "message": "Suggested. The user installs it or not in the app; do not say it is installed."}), nil
		})
		protocol.NewStreamableHTTPHandler(func(*http.Request) *protocol.Server { return server }, &protocol.StreamableHTTPOptions{Stateless: true, JSONResponse: true}).ServeHTTP(w, r)
	})
}

func (h *Host) botByName(ctx context.Context, name string) (*Bot, error) {
	bots, err := h.Store.ListBots(ctx)
	if err != nil {
		return nil, err
	}
	for _, bot := range bots {
		if strings.EqualFold(strings.TrimSpace(bot.Name), strings.TrimSpace(name)) {
			return &bot, nil
		}
	}
	return nil, fmt.Errorf("no bot called %q", name)
}

// --- HTTP ---

func (h *Host) listSkillSources(w http.ResponseWriter, r *http.Request) {
	items, err := h.Store.SkillSources(r.Context())
	writeResult(w, items, err)
}

func (h *Host) addSkillSource(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Repo string `json:"repo"`
	}
	if !decode(w, r, &input) {
		return
	}
	source, err := h.Store.AddSkillSource(r.Context(), input.Repo)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.IndexSource(r.Context(), source.ID); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("added, but could not read it: %w", err))
		return
	}
	source, err = h.Store.SkillSource(r.Context(), source.ID)
	writeResult(w, source, err)
}

func (h *Host) updateSkillSource(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	if err := h.Store.SetSkillSourceEnabled(r.Context(), id, input.Enabled); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if source, err := h.Store.SkillSource(r.Context(), id); err == nil && input.Enabled && source.IndexedAt == nil {
		if err := h.IndexSource(r.Context(), id); err != nil {
			writeError(w, http.StatusBadGateway, fmt.Errorf("turned on, but could not read it: %w", err))
			return
		}
	}
	source, err := h.Store.SkillSource(r.Context(), id)
	writeResult(w, source, err)
}

func (h *Host) removeSkillSource(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.RemoveSkillSource(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Host) refreshSkillSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.IndexSource(r.Context(), id); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	source, err := h.Store.SkillSource(r.Context(), id)
	writeResult(w, source, err)
}

func (h *Host) searchCatalog(w http.ResponseWriter, r *http.Request) {
	items, err := h.SearchCatalog(r.Context(), r.URL.Query().Get("q"), 200)
	writeResult(w, items, err)
}

func (h *Host) previewCatalogSkill(w http.ResponseWriter, r *http.Request) {
	content, err := h.CatalogPreview(r.Context(), r.URL.Query().Get("id"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": content})
}

func (h *Host) installCatalogSkill(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID    string `json:"id"`
		BotID string `json:"bot_id"`
	}
	if !decode(w, r, &input) {
		return
	}
	skill, err := h.InstallCatalogSkill(r.Context(), input.ID, input.BotID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, skill)
}
