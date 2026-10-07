import { useMemo } from "react";
import { resolveIconId, suggestIcons } from "./icons";

// Bot faces: Rex's own family of small robots. Every bot is one flat colour
// with a slightly irregular head, the same eyes, and one sign for its job
// (a pencil for writing, a coin for money). The job comes from the bot's
// icon. Faces are still; while a bot is working it breathes, blinks and its
// sign moves (see .bot-face in styles.css).

export type FaceJob = "bot" | "travel" | "writer" | "data" | "news" | "research" | "fitness" | "chef" | "money" | "inbox" | "code" | "planner" | "music";

const jobsByIcon: Record<string, FaceJob> = {};
const assign = (job: FaceJob, ids: string) => ids.split(" ").forEach(id => { jobsByIcon[id] = job; });
assign("travel", "plane luggage map palmtree mountain train car hotel earth compass");
assign("writer", "pen-line feather languages palette");
assign("data", "chart trending-up database");
assign("news", "newspaper megaphone");
assign("research", "search brain microscope flask book graduation-cap calculator lightbulb");
assign("fitness", "dumbbell footprints bike flower stethoscope pill moon");
assign("chef", "chef-hat salad coffee wine shopping-cart");
assign("money", "wallet credit-card landmark candlestick coins receipt");
assign("inbox", "mail handshake users smartphone");
assign("code", "code wrench workflow bug lock globe hammer");
assign("planner", "list-checks calendar briefcase folder target rocket scale");
assign("music", "headphones clapperboard camera gamepad");

/** Faces to choose from, with what each is for. */
export const faces: { job: FaceJob; label: string }[] = [
  { job: "bot", label: "General" }, { job: "travel", label: "Travel" }, { job: "writer", label: "Writing" }, { job: "data", label: "Data" },
  { job: "news", label: "News" }, { job: "research", label: "Research" }, { job: "fitness", label: "Health & fitness" }, { job: "chef", label: "Food" },
  { job: "money", label: "Money" }, { job: "inbox", label: "Email & people" }, { job: "code", label: "Code & tech" }, { job: "planner", label: "Plans & tasks" },
  { job: "music", label: "Music & media" },
];
const jobs = new Set<string>(faces.map(item => item.job));

/** Soft, slightly washed-out colours for a bot's face; the same list as botColors in internal/rex/setup.go. */
export const botColors = ["#8b93e8", "#e0a35e", "#e8876f", "#62b89c", "#6aa9de", "#a592dc", "#e48aa8", "#86b46e", "#58adbb", "#c58ad6", "#c9a77c", "#8e9bb0"];

/** The face for a bot's stored value: a face, or an icon or emoji from before faces. */
export function faceJob(value: string): FaceJob {
  if (jobs.has(value)) return value as FaceJob;
  return jobsByIcon[resolveIconId(value)] || "bot";
}

/** The face that fits a bot's name (and instructions). */
export function suggestFace(name: string, instructions = ""): FaceJob {
  for (const id of suggestIcons(name, instructions, 6)) if (jobsByIcon[id]) return jobsByIcon[id];
  return "bot";
}

type Head = { cx: number; cy: number; rx: number; ry: number; n: number; tilt: number; points?: number };
type Box = { top: number; left: number; right: number; cx: number; cy: number };
type Colors = { light: string; mid: string; deep: string };
type Sign = { back?: string; front?: string; noFace?: boolean; noMouth?: boolean; eyeDrop?: number };

// Heads: a superellipse (n 2 is round, higher is squarer), a little wobbly
// and tilted, different per job.
const heads: Record<FaceJob, Head> = {
  bot:      { cx: 50, cy: 59, rx: 28, ry: 24, n: 3.4, tilt: -2 },
  travel:   { cx: 50, cy: 60, rx: 30, ry: 22, n: 3.6, tilt: -3 },
  writer:   { cx: 50, cy: 59, rx: 23, ry: 26, n: 3.2, tilt: 4 },
  data:     { cx: 50, cy: 60, rx: 31, ry: 22, n: 5, tilt: -2 },
  news:     { cx: 50, cy: 62, rx: 35, ry: 20, n: 2.6, tilt: 2 },
  research: { cx: 50, cy: 60, rx: 25, ry: 25, n: 2.1, tilt: 0 },
  fitness:  { cx: 50, cy: 61, rx: 32, ry: 23, n: 4.2, tilt: 3 },
  chef:     { cx: 50, cy: 63, rx: 27, ry: 21, n: 2.8, tilt: -3 },
  money:    { cx: 50, cy: 61, rx: 24, ry: 24, n: 2.2, tilt: 0 },
  inbox:    { cx: 50, cy: 60, rx: 26, ry: 24, n: 3, tilt: 3 },
  code:     { cx: 50, cy: 60, rx: 27, ry: 25, n: 2, tilt: 0, points: 6 },
  planner:  { cx: 50, cy: 59, rx: 28, ry: 25, n: 4.6, tilt: -2 },
  music:    { cx: 50, cy: 60, rx: 24, ry: 23, n: 3, tilt: 2 },
};

function seeded(text: string): () => number {
  let state = [...text].reduce((acc, char) => (acc * 31 + char.charCodeAt(0)) >>> 0, 7);
  return () => ((state = (state * 1664525 + 1013904223) >>> 0) / 2 ** 32) * 2 - 1;
}

const f = (value: number) => value.toFixed(1);

function headPath(job: FaceJob, h: Head): string {
  const rand = seeded(job), count = h.points || 12, wobble = h.points ? 0.04 : 0.07;
  const pts = Array.from({ length: count }, (_, i) => {
    const t = 2 * Math.PI * i / count - (h.points ? Math.PI / 2 : 0);
    const c = Math.cos(t), s = Math.sin(t), k = 1 + rand() * wobble;
    return [h.cx + h.rx * k * Math.sign(c) * Math.abs(c) ** (2 / h.n), h.cy + h.ry * k * Math.sign(s) * Math.abs(s) ** (2 / h.n)];
  });
  // A closed Catmull-Rom spline through the points, as cubic curves.
  const tension = h.points ? 0.12 : 1 / 6;
  let d = `M${f(pts[0][0])} ${f(pts[0][1])}`;
  for (let i = 0; i < count; i++) {
    const [p0, p1, p2, p3] = [-1, 0, 1, 2].map(o => pts[(i + o + count) % count]);
    d += ` C${f(p1[0] + (p2[0] - p0[0]) * tension)} ${f(p1[1] + (p2[1] - p0[1]) * tension)} ${f(p2[0] - (p3[0] - p1[0]) * tension)} ${f(p2[1] - (p3[1] - p1[1]) * tension)} ${f(p2[0])} ${f(p2[1])}`;
  }
  return d + "Z";
}

function shade(hex: string, amount: number): string {
  const target = amount > 0 ? 255 : 0, weight = Math.abs(amount);
  return "#" + [1, 3, 5].map(i => {
    const channel = parseInt(hex.slice(i, i + 2), 16);
    return Math.round(channel + (target - channel) * weight).toString(16).padStart(2, "0");
  }).join("");
}

const WHITE = "#ffffff", GOLD = "#f2b631", PENCIL = "#f6c445";

function face(b: Box, deep: string, sign: Sign): string {
  const ey = b.cy - 3 + (sign.eyeDrop || 0), gap = 11;
  const eye = (x: number) => `<rect x="${x - 5}" y="${ey - 7}" width="10" height="14" rx="5" fill="${WHITE}"/><circle class="look" cx="${x + 1}" cy="${ey - 1}" r="2.6" fill="${deep}"/>`;
  const mouth = sign.noMouth ? "" : `<path d="M${b.cx - 6} ${ey + 13} Q${b.cx} ${ey + 18} ${b.cx + 6} ${ey + 13}" fill="none" stroke="${WHITE}" stroke-width="3" stroke-linecap="round"/>`;
  return `<g class="blink">${eye(b.cx - gap)}${eye(b.cx + gap)}</g>${mouth}`;
}

function antenna(b: Box, c: Colors): string {
  return `<g class="wobble"><path d="M${b.cx} ${b.top + 2} V${b.top - 10}" stroke="${c.deep}" stroke-width="3.5" stroke-linecap="round"/><circle class="bulb" cx="${b.cx}" cy="${b.top - 14}" r="5.5" fill="${c.light}"/></g>`;
}

// One sign per job, drawn behind (back) or over (front) the head.
const signs: Record<FaceJob, (b: Box, c: Colors) => Sign> = {
  bot: (b, c) => ({ back: antenna(b, c) }),
  travel: (b, c) => ({
    back: `<g class="fly"><path d="M50 4 L53.5 14 L66 19 V23 L53.5 21 L52.5 28 L57 30.5 V33 L50 32 L43 33 V30.5 L47.5 28 L46.5 21 L34 23 V19 L46.5 14 Z" fill="${c.deep}"/></g>`,
    front: `<path d="M${b.left} ${b.cy - 2} H${b.right}" stroke="${c.deep}" stroke-width="4"/>
      <circle cx="${b.cx - 11}" cy="${b.cy - 2}" r="9.5" fill="${WHITE}" stroke="${c.deep}" stroke-width="3.6"/><circle cx="${b.cx + 11}" cy="${b.cy - 2}" r="9.5" fill="${WHITE}" stroke="${c.deep}" stroke-width="3.6"/>
      <g class="blink"><circle class="look" cx="${b.cx - 10}" cy="${b.cy - 3}" r="3.2" fill="${c.deep}"/><circle class="look" cx="${b.cx + 12}" cy="${b.cy - 3}" r="3.2" fill="${c.deep}"/></g>
      <path d="M${b.cx - 6} ${b.cy + 13} Q${b.cx} ${b.cy + 18} ${b.cx + 6} ${b.cy + 13}" fill="none" stroke="${WHITE}" stroke-width="3" stroke-linecap="round"/>`,
    noFace: true,
  }),
  writer: () => ({
    back: `<g class="scribble"><g transform="translate(63 40) rotate(48) scale(.88) translate(-66 -18.5)"><rect x="61" y="0" width="10" height="27" rx="2.5" fill="${PENCIL}"/><rect x="61" y="0" width="10" height="5" rx="2" fill="#ef8fa6"/><rect x="61" y="5" width="10" height="2.5" fill="#c9c9cf"/><path d="M61 27 H71 L66 37 Z" fill="#f3d9a8"/><path d="M64 33 H68 L66 37 Z" fill="#2b2b2f"/></g></g>`,
  }),
  data: (b, c) => ({
    back: antenna(b, c),
    front: `<rect x="${b.cx - 18}" y="${b.cy - 12}" width="36" height="24" rx="4" fill="${c.deep}"/>
      <rect class="bar" x="${b.cx - 13}" y="${b.cy}" width="6" height="8" rx="1.5" fill="${WHITE}"/><rect class="bar b2" x="${b.cx - 3}" y="${b.cy - 8}" width="6" height="16" rx="1.5" fill="${WHITE}"/><rect class="bar b3" x="${b.cx + 7}" y="${b.cy - 4}" width="6" height="12" rx="1.5" fill="${WHITE}"/>`,
    noFace: true,
  }),
  news: (_b, c) => ({
    back: `<g class="tip"><path d="M20 44 L50 12 L80 44 Z" fill="${WHITE}" stroke="${c.deep}" stroke-width="2.5" stroke-linejoin="round"/><path d="M33 33 H67 M38 27 H62" stroke="${c.mid}" stroke-width="2.4" stroke-linecap="round"/></g>`,
  }),
  research: (b, c) => ({
    back: antenna(b, c),
    front: `<g class="scan"><path d="M${b.cx + 18} ${b.cy + 8} L${b.cx + 32} ${b.cy + 22}" stroke="${c.deep}" stroke-width="7" stroke-linecap="round"/>
      <circle cx="${b.cx + 10}" cy="${b.cy - 3}" r="11" fill="${WHITE}" stroke="${c.deep}" stroke-width="4"/><circle class="look" cx="${b.cx + 11}" cy="${b.cy - 3}" r="4" fill="${c.deep}"/></g>
      <g class="blink"><rect x="${b.cx - 16}" y="${b.cy - 10}" width="10" height="14" rx="5" fill="${WHITE}"/><circle class="look" cx="${b.cx - 10}" cy="${b.cy - 4}" r="2.6" fill="${c.deep}"/></g>
      <path d="M${b.cx - 10} ${b.cy + 13} H${b.cx}" stroke="${WHITE}" stroke-width="3" stroke-linecap="round"/>`,
    noFace: true,
  }),
  fitness: (b, c) => ({
    back: `<g class="lift"><rect x="33" y="21" width="34" height="5" rx="2.5" fill="${c.deep}"/><rect x="24" y="12" width="9" height="23" rx="3.5" fill="${c.deep}"/><rect x="67" y="12" width="9" height="23" rx="3.5" fill="${c.deep}"/></g>`,
    front: `<rect x="${b.left}" y="${b.top + 5}" width="${b.right - b.left}" height="7" fill="${WHITE}" opacity=".92"/>`,
    eyeDrop: 4,
  }),
  chef: (_b, c) => ({
    back: `<g class="puff"><circle cx="38" cy="25" r="11" fill="${WHITE}"/><circle cx="50" cy="18" r="12.5" fill="${WHITE}"/><circle cx="62" cy="25" r="11" fill="${WHITE}"/><rect x="31" y="24" width="38" height="18" rx="3" fill="${WHITE}"/><path d="M31 37 H69" stroke="${c.mid}" stroke-width="2.4"/></g>`,
    eyeDrop: 2,
  }),
  money: (b, c) => ({
    back: `<path d="M50 ${b.top + 2} V${b.top - 6}" stroke="${c.deep}" stroke-width="3.5" stroke-linecap="round"/><g class="coin"><circle cx="50" cy="18" r="12.5" fill="${GOLD}"/><circle cx="50" cy="18" r="9" fill="none" stroke="#c98a12" stroke-width="2"/><text x="50" y="23.5" text-anchor="middle" font-family="Arial, sans-serif" font-weight="700" font-size="14" fill="#8a5b00">$</text></g>`,
  }),
  inbox: (b, c) => ({
    back: `<path d="M23 56 Q23 22 50 22 Q77 22 77 56" fill="none" stroke="${c.deep}" stroke-width="5" stroke-linecap="round"/>`,
    front: `<rect x="15" y="50" width="12" height="20" rx="5" fill="${c.deep}"/><rect x="73" y="50" width="12" height="20" rx="5" fill="${c.deep}"/>
      <path d="M21 68 Q23 80 41 78" fill="none" stroke="${c.deep}" stroke-width="3" stroke-linecap="round"/><circle class="bulb" cx="42" cy="78" r="3.4" fill="${c.deep}"/>
      <g class="talk"><path d="M${b.cx - 5} ${b.cy + 11} Q${b.cx} ${b.cy + 15} ${b.cx + 5} ${b.cy + 11}" fill="none" stroke="${WHITE}" stroke-width="3" stroke-linecap="round"/></g>`,
    noMouth: true,
  }),
  code: (b, c) => ({
    back: antenna(b, c),
    front: `<path class="brl" d="M${b.cx - 8} ${b.cy - 10} L${b.cx - 16} ${b.cy - 3} L${b.cx - 8} ${b.cy + 4}" fill="none" stroke="${WHITE}" stroke-width="4.4" stroke-linecap="round" stroke-linejoin="round"/>
      <path class="brr" d="M${b.cx + 8} ${b.cy - 10} L${b.cx + 16} ${b.cy - 3} L${b.cx + 8} ${b.cy + 4}" fill="none" stroke="${WHITE}" stroke-width="4.4" stroke-linecap="round" stroke-linejoin="round"/>
      <path class="cursor" d="M${b.cx - 5} ${b.cy + 13} H${b.cx + 5}" stroke="${WHITE}" stroke-width="3" stroke-linecap="round"/>`,
    noFace: true,
  }),
  planner: (b, c) => ({
    back: `<g class="ring"><rect x="33" y="24" width="7" height="18" rx="3.5" fill="${c.deep}"/></g><g class="ring r2"><rect x="60" y="24" width="7" height="18" rx="3.5" fill="${c.deep}"/></g>`,
    front: `<rect x="${b.left}" y="${b.top}" width="${b.right - b.left}" height="12" rx="9" fill="${c.deep}"/><rect x="${b.left}" y="${b.top + 6}" width="${b.right - b.left}" height="6" fill="${c.deep}"/>`,
    eyeDrop: 5,
  }),
  music: (b, c) => ({
    back: `<g class="note"><path d="M80 14 V2 L88 0 V11" fill="none" stroke="${c.mid}" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"/><circle cx="77.5" cy="14.5" r="3.4" fill="${c.mid}"/><circle cx="85.5" cy="11.5" r="3.4" fill="${c.mid}"/></g>
      <path d="M20 60 Q20 20 50 20 Q80 20 80 60" fill="none" stroke="${c.deep}" stroke-width="6" stroke-linecap="round"/>`,
    front: `<rect x="12" y="51" width="15" height="23" rx="6.5" fill="${c.deep}"/><rect x="73" y="51" width="15" height="23" rx="6.5" fill="${c.deep}"/>
      <path d="M${b.cx - 15} ${b.cy - 2} Q${b.cx - 11} ${b.cy - 7} ${b.cx - 7} ${b.cy - 2}" fill="none" stroke="${WHITE}" stroke-width="3.6" stroke-linecap="round"/>
      <path d="M${b.cx + 7} ${b.cy - 2} Q${b.cx + 11} ${b.cy - 7} ${b.cx + 15} ${b.cy - 2}" fill="none" stroke="${WHITE}" stroke-width="3.6" stroke-linecap="round"/>
      <path d="M${b.cx - 7} ${b.cy + 9} Q${b.cx} ${b.cy + 16} ${b.cx + 7} ${b.cy + 9}" fill="none" stroke="${WHITE}" stroke-width="3" stroke-linecap="round"/>`,
    noFace: true,
  }),
};

/** The SVG markup for a face. Built only from these drawings and a checked colour. */
export function faceSVG(job: FaceJob, color: string): string {
  const fill = /^#[0-9a-f]{6}$/i.test(color) ? color : "#7a7f8c";
  const h = heads[job];
  const b: Box = { top: h.cy - h.ry, left: h.cx - h.rx, right: h.cx + h.rx, cx: h.cx, cy: h.cy };
  const c: Colors = { light: shade(fill, 0.32), mid: shade(fill, -0.1), deep: shade(fill, -0.42) };
  const sign = signs[job](b, c);
  return `<svg viewBox="9 5 82 82" width="100%" height="100%" overflow="visible"><g transform="rotate(${h.tilt} 50 55)"><g class="body">${sign.back || ""}<path d="${headPath(job, h)}" fill="${fill}"/>${sign.noFace ? "" : face(b, c.deep, sign)}${sign.front || ""}</g></g></svg>`;
}

/** A bot's face. Still, unless the bot is working; each bot keeps its own timing so a list never moves in step. */
export function BotFace({ icon, color, seed, size, working = false }: { icon: string; color: string; seed: string; size: number; working?: boolean }) {
  const job = faceJob(icon);
  const markup = useMemo(() => faceSVG(job, color), [job, color]);
  const timing = useMemo(() => {
    const rand = seeded(seed || "bot");
    return { "--delay": `${(-Math.abs(rand()) * 5).toFixed(2)}s`, "--blink": `${(3.5 + Math.abs(rand()) * 2.5).toFixed(2)}s`, "--bob": `${(2.4 + Math.abs(rand()) * 1.2).toFixed(2)}s` } as React.CSSProperties;
  }, [seed]);
  return <span className={`bot-face face-${job} ${working ? "working" : ""}`} style={{ width: size, height: size, ...timing }} aria-hidden dangerouslySetInnerHTML={{ __html: markup }} />;
}
