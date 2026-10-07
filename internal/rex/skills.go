package rex

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Skills follow the Agent Skills format: a folder with a SKILL.md (front
// matter with name and description, then instructions) and any supporting
// files such as scripts/ or references/. They live in ~/Rex/skills so
// users can also add and edit them in Finder or any editor. Rex hands
// attached skills to the agent runtime, which adds their instructions to
// the bot and copies the folder into the run for read_skill and scripts.

type Skill struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// Body is the whole SKILL.md, front matter included.
	Body string `json:"body"`
	// Files are the skill's other files, relative to its folder.
	Files     []string  `json:"files"`
	Version   string    `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SkillLibrary keeps skills as folders under one directory.
type SkillLibrary struct{ Dir string }

const (
	skillFile           = "SKILL.md"
	maxSkillBytes       = 10 << 20 // a whole skill folder or imported zip
	maxSkillFiles       = 200
	maxSkillInstruction = 64 << 10
)

const defaultSkill = `---
name: research-report
description: Research a topic from several reputable sources and write a short report with citations. Use when the user asks to research, investigate or compare something.
---

# Research report

1. Search for and read at least three independent, reputable sources.
2. Note each source's title and URL as you go.
3. Write the report to outputs/<short-topic>.md with sections: Summary, Key findings, Open questions, Sources.
4. Reply with a three-sentence summary and the file path.
`

type skillFrontMatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// splitFrontMatter separates YAML front matter from the instructions.
func splitFrontMatter(content string) (skillFrontMatter, string, bool) {
	var front skillFrontMatter
	content = strings.TrimLeft(content, "\ufeff\n\r ")
	if !strings.HasPrefix(content, "---") {
		return front, content, false
	}
	rest := strings.TrimLeft(content[3:], "\r")
	rest = strings.TrimPrefix(rest, "\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return front, content, false
	}
	if yaml.Unmarshal([]byte(rest[:end]), &front) != nil {
		return front, content, false
	}
	body := strings.TrimLeft(rest[end+4:], "-\r")
	return front, strings.TrimSpace(body), true
}

func (l SkillLibrary) EnsureDefaults() error {
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return err
	}
	if err := l.migrateFlatFiles(); err != nil {
		return err
	}
	entries, err := os.ReadDir(l.Dir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return l.write("research-report", defaultSkill)
	}
	return nil
}

// migrateFlatFiles turns the old one-file skills (<id>.md with a "# Title"
// heading) into skill folders.
func (l SkillLibrary) migrateFlatFiles() error {
	entries, err := os.ReadDir(l.Dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".md")
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") || id != Slug(id) {
			continue
		}
		old := filepath.Join(l.Dir, entry.Name())
		raw, err := os.ReadFile(old)
		if err != nil {
			return err
		}
		content := strings.TrimLeft(string(raw), "\ufeff\n ")
		title := id
		if strings.HasPrefix(content, "# ") {
			line, rest, _ := strings.Cut(content, "\n")
			title, content = strings.TrimSpace(strings.TrimPrefix(line, "# ")), strings.TrimSpace(rest)
		}
		if _, _, ok := splitFrontMatter(content); !ok {
			content = fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", id, yamlString(title), content)
		}
		if _, err := os.Stat(filepath.Join(l.Dir, id)); err == nil {
			continue
		}
		if err := l.write(id, content); err != nil {
			return err
		}
		if err := os.Remove(old); err != nil {
			return err
		}
	}
	return nil
}

func yamlString(value string) string {
	out, _ := yaml.Marshal(value)
	return strings.TrimSpace(string(out))
}

func (l SkillLibrary) folder(id string) (string, error) {
	if id == "" || id != Slug(id) {
		return "", fmt.Errorf("invalid skill id %q", id)
	}
	return filepath.Join(l.Dir, id), nil
}

func (l SkillLibrary) write(id, content string) error {
	dir, err := l.folder(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, skillFile), []byte(strings.TrimSpace(content)+"\n"), 0o644)
}

func (l SkillLibrary) List() ([]Skill, error) {
	_ = l.migrateFlatFiles()
	entries, err := os.ReadDir(l.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Skill{}, nil
	}
	if err != nil {
		return nil, err
	}
	skills := []Skill{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if skill, err := l.Get(entry.Name()); err == nil {
			skills = append(skills, *skill)
		}
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Title < skills[j].Title })
	return skills, nil
}

// skillFiles walks a skill folder: regular files only, no symlinks, within
// the size and count limits.
func skillFiles(dir string) ([]string, error) {
	var files []string
	var total int64
	err := filepath.WalkDir(dir, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") && current != dir {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		relative, _ := filepath.Rel(dir, current)
		files = append(files, filepath.ToSlash(relative))
		if len(files) > maxSkillFiles || total > maxSkillBytes {
			return fmt.Errorf("a skill can hold at most %d files and %d MB", maxSkillFiles, maxSkillBytes>>20)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func (l SkillLibrary) Get(id string) (*Skill, error) {
	dir, err := l.folder(id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, skillFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	files, err := skillFiles(dir)
	if err != nil {
		return nil, err
	}
	front, body, _ := splitFrontMatter(string(raw))
	title := strings.TrimSpace(front.Name)
	if heading := firstHeading(body); heading != "" {
		title = heading
	}
	if title == "" {
		title = id
	}
	skill := &Skill{ID: id, Title: title, Description: strings.TrimSpace(front.Description), Body: string(raw)}
	hash := sha256.New()
	for _, file := range files {
		if file != skillFile {
			skill.Files = append(skill.Files, file)
		}
		content, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(file)))
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(hash, "%s\x00%d\x00", file, len(content))
		hash.Write(content)
		if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(file))); err == nil && info.ModTime().After(skill.UpdatedAt) {
			skill.UpdatedAt = info.ModTime()
		}
	}
	skill.Version = hex.EncodeToString(hash.Sum(nil))[:12]
	if skill.Files == nil {
		skill.Files = []string{}
	}
	return skill, nil
}

func firstHeading(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
		if strings.TrimSpace(line) != "" {
			return ""
		}
	}
	return ""
}

// Save writes a skill's SKILL.md. Text without front matter gets one, so
// every skill has the name and description agents use to pick it.
func (l SkillLibrary) Save(skill Skill) (*Skill, error) {
	content := strings.TrimSpace(skill.Body)
	front, body, ok := splitFrontMatter(content)
	if strings.TrimSpace(body) == "" {
		return nil, errors.New("skill instructions are required")
	}
	if len(content) > maxSkillInstruction {
		return nil, fmt.Errorf("SKILL.md must be at most %d KB; put longer material in files next to it", maxSkillInstruction>>10)
	}
	id := skill.ID
	if id == "" {
		id = Slug(firstNonEmpty(front.Name, skill.Title, firstHeading(body)))
	}
	if id == "" {
		return nil, errors.New("give the skill a name")
	}
	if !ok {
		description := firstNonEmpty(skill.Description, skill.Title, firstHeading(body), id)
		content = fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s", id, yamlString(description), body)
	} else if strings.TrimSpace(front.Description) == "" {
		return nil, errors.New("the front matter needs a description: it tells the bot when to use the skill")
	}
	if err := l.write(id, content); err != nil {
		return nil, err
	}
	return l.Get(id)
}

func (l SkillLibrary) Delete(id string) error {
	dir, err := l.folder(id)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// Resolve loads enabled skills, skipping any that were deleted.
func (l *SkillLibrary) Resolve(ids []string) []Skill {
	if l == nil {
		return nil
	}
	var skills []Skill
	for _, id := range ids {
		if skill, err := l.Get(id); err == nil {
			skills = append(skills, *skill)
		}
	}
	return skills
}

// Package zips a skill folder under one root directory, the layout the
// agent runtime stages.
func (l SkillLibrary) Package(id string) ([]byte, error) {
	dir, err := l.folder(id)
	if err != nil {
		return nil, err
	}
	files, err := skillFiles(dir)
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for _, file := range files {
		content, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(file)))
		if err != nil {
			return nil, err
		}
		writer, err := archive.CreateHeader(&zip.FileHeader{Name: path.Join(id, file), Method: zip.Deflate})
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write(content); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// Import adds a skill from a zip: one SKILL.md (at the top or in one
// folder) plus its files. It refuses unsafe paths, links and oversize
// archives, and replaces a skill with the same name.
func (l SkillLibrary) Import(data []byte) (*Skill, error) {
	if len(data) > maxSkillBytes {
		return nil, fmt.Errorf("a skill zip can be at most %d MB", maxSkillBytes>>20)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("this is not a zip file")
	}
	roots := map[string]bool{}
	for _, file := range reader.File {
		if path.Base(file.Name) == skillFile && !file.FileInfo().IsDir() {
			roots[path.Dir(file.Name)] = true
		}
	}
	if len(roots) != 1 {
		return nil, errors.New("the zip must contain exactly one SKILL.md")
	}
	var root string
	for candidate := range roots {
		root = candidate
	}
	var skillMD []byte
	contents := map[string][]byte{}
	var total int64
	for _, file := range reader.File {
		name := file.Name
		if file.FileInfo().IsDir() || strings.HasPrefix(path.Base(name), ".") || strings.Contains(name, "__MACOSX") {
			continue
		}
		if file.Mode()&fs.ModeSymlink != 0 {
			return nil, errors.New("skills cannot contain links")
		}
		relative := name
		if root != "." {
			if !strings.HasPrefix(name, root+"/") {
				continue
			}
			relative = strings.TrimPrefix(name, root+"/")
		}
		clean := path.Clean(relative)
		if clean == "." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) || strings.Contains(clean, "\\") {
			return nil, fmt.Errorf("unsafe path in the zip: %s", name)
		}
		opened, err := file.Open()
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(io.LimitReader(opened, maxSkillBytes+1))
		opened.Close()
		if err != nil {
			return nil, err
		}
		total += int64(len(content))
		if total > maxSkillBytes || len(contents) >= maxSkillFiles {
			return nil, fmt.Errorf("a skill can hold at most %d files and %d MB", maxSkillFiles, maxSkillBytes>>20)
		}
		contents[clean] = content
		if clean == skillFile {
			skillMD = content
		}
	}
	front, body, ok := splitFrontMatter(string(skillMD))
	if !ok || strings.TrimSpace(front.Description) == "" || strings.TrimSpace(body) == "" {
		return nil, errors.New("SKILL.md needs front matter with a name and description, then instructions")
	}
	id := Slug(firstNonEmpty(front.Name, path.Base(root)))
	dir, err := l.folder(id)
	if err != nil {
		return nil, err
	}
	staging, err := os.MkdirTemp(l.Dir, ".import-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	for name, content := range contents {
		target := filepath.Join(staging, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return nil, err
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if err := os.Rename(staging, dir); err != nil {
		return nil, err
	}
	return l.Get(id)
}
