// A skill is one SKILL.md: front matter (name, description, and any other
// keys the skill came with), an optional "# Title" heading and the
// instructions. The Skills page edits these as separate fields and writes the
// file back, keeping keys it does not show.

export type SkillDoc = {
  name: string;
  description: string;
  title: string;
  instructions: string;
  // Other front matter lines, kept as they were (license, allowed-tools…).
  extra: string[];
};

export function parseSkillDoc(text: string): SkillDoc {
  const doc: SkillDoc = { name: "", description: "", title: "", instructions: "", extra: [] };
  let body = text.replace(/^﻿/, "");
  const front = /^---[ \t]*\r?\n([\s\S]*?)\r?\n---[ \t]*(?:\r?\n|$)/.exec(body);
  if (front) {
    body = body.slice(front[0].length);
    const lines = front[1].split(/\r?\n/);
    for (let index = 0; index < lines.length; index++) {
      const match = /^([A-Za-z0-9_-]+):[ \t]*(.*)$/.exec(lines[index]);
      // Indented lines that follow a key belong to it.
      const block: string[] = [];
      while (index + 1 < lines.length && /^\s+\S|^\s*$/.test(lines[index + 1]) && !/^[A-Za-z0-9_-]+:/.test(lines[index + 1])) block.push(lines[++index]);
      if (match && (match[1] === "name" || match[1] === "description")) {
        doc[match[1]] = yamlValue(match[2], block);
      } else {
        doc.extra.push(lines[index - block.length], ...block);
      }
    }
  }
  body = body.replace(/^\s*\n/, "");
  const heading = /^#[ \t]+(.+?)[ \t]*(?:\r?\n|$)/.exec(body.trimStart());
  if (heading) {
    doc.title = heading[1].trim();
    body = body.trimStart().slice(heading[0].length);
  }
  doc.instructions = body.replace(/^\s*\n/, "").trimEnd();
  return doc;
}

function yamlValue(value: string, block: string[]): string {
  value = value.trim();
  if (/^[|>][+-]?$/.test(value)) {
    const lines = block.map(line => line.trim());
    return (value.startsWith(">") ? lines.filter(Boolean).join(" ") : lines.join("\n")).trim();
  }
  if (block.length) value = [value, ...block.map(line => line.trim())].filter(Boolean).join(" ");
  if (value.startsWith("\"")) {
    try { return JSON.parse(value); } catch { return value.slice(1, -1); }
  }
  if (value.startsWith("'") && value.endsWith("'")) return value.slice(1, -1).replace(/''/g, "'");
  return value;
}

function yamlString(value: string): string {
  const text = value.replace(/\s+/g, " ").trim();
  return /^[\s\-?:,[\]{}#&*!|>'"%@`]|: |\s#|^(true|false|null|yes|no|~|\d[\d.]*)$/i.test(text) ? JSON.stringify(text) : text;
}

export function composeSkillDoc(doc: SkillDoc): string {
  const front = ["---", `name: ${doc.name}`, `description: ${yamlString(doc.description)}`, ...doc.extra, "---"].join("\n");
  const title = doc.title.trim() ? `# ${doc.title.trim()}\n\n` : "";
  return `${front}\n\n${title}${doc.instructions.trim()}\n`;
}

/** A folder name for a new skill, as the host makes them. */
export function skillSlug(value: string): string {
  const slug = value.toLowerCase().normalize("NFKD").replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
  return slug.slice(0, 40).replace(/-+$/, "");
}

/** A readable title from a folder name: "research-report" → "Research report". */
export function titleFromName(name: string): string {
  const words = name.replace(/[-_]+/g, " ").trim();
  return words ? words[0].toUpperCase() + words.slice(1) : "";
}
