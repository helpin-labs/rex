package rex

import (
	"net/http"
	"strings"
)

// The agent runtime fetches skills from the host (its "skill provider"):
// it looks a skill up by id or name, then downloads its folder as a zip.
// Rex serves both from ~/Rex/skills, so skills edited in Finder apply to
// the next run.

// SkillRef points an agent at a skill in Rex's library.
type SkillRef struct {
	SkillID string `json:"skill_id,omitempty"`
	Key     string `json:"key,omitempty"`
}

// runtimeSkill is the runtime's WorkspaceSkill.
type runtimeSkill struct {
	ID               string `json:"id"`
	Key              string `json:"key"`
	VersionKey       string `json:"version_key"`
	Title            string `json:"title"`
	Description      string `json:"description"`
	SourceKind       string `json:"source_kind"`
	Instructions     string `json:"instructions"`
	PackageObjectKey string `json:"package_object_key"`
	PackageFileName  string `json:"package_file_name"`
}

func skillPackageKey(skill *Skill) string { return "rex/" + skill.ID + "/" + skill.Version + ".zip" }

func (h *Host) lookupSkill(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SkillID string `json:"skill_id"`
		Key     string `json:"key"`
	}
	if !decode(w, r, &input) {
		return
	}
	skill, err := h.Skills.Get(firstNonEmpty(input.SkillID, input.Key))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_, body, _ := splitFrontMatter(skill.Body)
	writeJSON(w, http.StatusOK, runtimeSkill{
		ID: skill.ID, Key: skill.ID, VersionKey: skill.Version, Title: skill.Title, Description: skill.Description,
		SourceKind: "workspace", Instructions: body, PackageObjectKey: skillPackageKey(skill), PackageFileName: skill.ID + ".zip",
	})
}

func (h *Host) skillPackage(w http.ResponseWriter, r *http.Request) {
	// Object keys look like rex/<skill>/<version>.zip; the current folder
	// is served, so a skill edited since the lookup is still consistent.
	parts := strings.Split(strings.TrimPrefix(r.PathValue("key"), "rex/"), "/")
	if len(parts) == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	archive, err := h.Skills.Package(parts[0])
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	_, _ = w.Write(archive)
}

// skillRefs lists a bot's skills for its runtime agent. Without a role the
// runtime adds each skill's instructions to the bot and stages its files.
func (b Bot) skillRefs() []SkillRef {
	refs := make([]SkillRef, 0, len(b.Skills))
	for _, id := range b.Skills {
		refs = append(refs, SkillRef{SkillID: id, Key: id})
	}
	return refs
}
