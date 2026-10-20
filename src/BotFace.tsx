import { useMemo } from "react";
import { resolveIconId, suggestIcons } from "./icons";

// The downloaded Blob Bots reference supplies the first eight silhouettes;
// the rest extend its flat colour, pill eyes, highlight and one-sign vocabulary.
export const faces = [
  { job: "bot", label: "General" }, { job: "travel", label: "Travel" }, { job: "writer", label: "Writing" },
  { job: "data", label: "Data" }, { job: "news", label: "News" }, { job: "research", label: "Research" },
  { job: "fitness", label: "Health & fitness" }, { job: "chef", label: "Food" }, { job: "money", label: "Money" },
  { job: "inbox", label: "Email & people" }, { job: "code", label: "Code & tech" }, { job: "planner", label: "Plans & tasks" },
  { job: "music", label: "Music & media" }, { job: "story", label: "Story planning" }, { job: "review", label: "QA & review" },
  { job: "security", label: "Security" }, { job: "design", label: "Design" }, { job: "sales", label: "Sales" },
  { job: "learning", label: "Learning" }, { job: "automation", label: "Automation" },
] as const;
export type FaceJob = typeof faces[number]["job"];
const jobs = new Set<string>(faces.map(item => item.job));

const jobsByIcon: Record<string, FaceJob> = {};
const assign = (job: FaceJob, ids: string) => ids.split(" ").forEach(id => { jobsByIcon[id] = job; });
assign("travel", "plane luggage map palmtree mountain train car hotel earth compass");
assign("writer", "pen-line feather languages");
assign("data", "chart database calculator");
assign("news", "newspaper megaphone");
assign("research", "search brain microscope flask lightbulb");
assign("fitness", "dumbbell footprints bike flower stethoscope pill moon heart");
assign("chef", "chef-hat salad coffee wine shopping-cart");
assign("money", "wallet credit-card landmark candlestick coins receipt");
assign("inbox", "mail users smartphone");
assign("code", "code wrench globe hammer");
assign("planner", "list-checks calendar briefcase folder target rocket scale");
assign("music", "headphones clapperboard camera gamepad");
assign("review", "bug");
assign("security", "lock");
assign("design", "palette");
assign("sales", "handshake trending-up");
assign("learning", "book graduation-cap");
assign("automation", "workflow");

// Keep the previous colours selectable for saved bots; shared with setup.go.
export const botColors = ["#e8613a", "#4f6e9c", "#d9a85b", "#c2382a", "#3e8e87", "#f2845e", "#8a5a8c", "#4f7a5a", "#8b93e8", "#e0a35e", "#e8876f", "#62b89c", "#6aa9de", "#a592dc", "#e48aa8", "#86b46e", "#58adbb", "#c58ad6", "#c9a77c", "#8e9bb0"];

export function faceJob(value: string): FaceJob {
  if (jobs.has(value)) return value as FaceJob;
  return jobsByIcon[resolveIconId(value)] || "bot";
}

export function suggestFace(name: string, instructions = ""): FaceJob {
  const text = `${name} ${instructions}`.toLowerCase();
  if (/\b(story|stories|scribe|backlog)\b/.test(text)) return "story";
  for (const id of suggestIcons(name, instructions, 6)) if (jobsByIcon[id]) return jobsByIcon[id];
  return "bot";
}

function seeded(text: string): () => number {
  let state = [...text].reduce((acc, char) => (acc * 31 + char.charCodeAt(0)) >>> 0, 7);
  return () => ((state = (state * 1664525 + 1013904223) >>> 0) / 2 ** 32) * 2 - 1;
}


const INK = "#1e1c1a", WHITE = "#ffffff", GOLD = "#d9a85b", CORAL = "#f2845e";
type Head = { path: string; shine: [number, number, number, number]; shadow: number };
const heads: Record<FaceJob, Head> = {
  bot: { path: "M80 18 C118 18 144 44 142 84 C140 122 114 144 78 142 C42 140 18 118 18 82 C18 46 44 18 80 18Z", shine: [50,44,14,-32], shadow: 46 },
  planner: { path: "M80 30 C124 26 150 56 146 92 C142 128 110 140 78 140 C40 140 12 124 14 90 C16 56 40 32 80 30Z", shine: [46,54,14,-28], shadow: 50 },
  story: { path: "M84 12 C116 16 132 50 128 88 C124 128 106 148 74 146 C38 144 26 112 30 76 C34 38 54 10 84 12Z", shine: [56,38,12,-40], shadow: 40 },
  code: { path: "M50 40 C56 22 82 18 96 30 C112 18 138 30 136 52 C150 64 148 92 132 104 C134 128 110 146 84 138 C66 150 34 142 30 120 C12 110 12 82 26 70 C20 54 32 40 50 40Z", shine: [54,52,12,-30], shadow: 48 },
  review: { path: "M44 22 C64 16 98 16 118 22 C138 28 142 46 142 80 C142 116 138 132 116 140 C96 146 62 146 42 140 C22 134 18 116 18 80 C18 46 24 28 44 22Z", shine: [42,40,12,-35], shadow: 50 },
  inbox: { path: "M80 14 C108 14 122 40 128 64 C140 100 136 146 80 146 C26 146 20 104 32 66 C40 40 52 14 80 14Z", shine: [58,38,11,-42], shadow: 48 },
  writer: { path: "M80 16 C100 16 108 32 124 36 C142 42 146 66 138 82 C132 96 146 112 132 130 C118 146 98 140 82 144 C64 148 44 146 32 130 C20 114 26 98 20 82 C14 64 24 44 42 36 C56 30 62 16 80 16Z", shine: [46,50,12,-34], shadow: 50 },
  security: { path: "M80 12 C96 34 136 62 136 100 C136 128 112 148 80 148 C48 148 24 128 24 100 C24 62 64 34 80 12Z", shine: [58,64,11,-48], shadow: 46 },
  travel: { path: "M52 32 C78 18 122 26 140 56 C156 86 138 126 102 140 C66 154 26 136 18 108 C8 76 26 44 52 32Z", shine: [44,54,13,-36], shadow: 48 },
  data: { path: "M48 26 C68 22 102 22 120 30 C138 38 138 62 142 86 C148 122 130 142 100 144 L54 144 C24 144 12 124 18 96 L24 50 C28 34 34 28 48 26Z", shine: [43,48,12,-30], shadow: 49 },
  news: { path: "M36 48 C58 28 112 32 132 52 C150 70 152 110 130 130 C108 150 56 146 30 130 C8 116 12 70 36 48Z", shine: [40,64,12,-28], shadow: 50 },
  research: { path: "M74 20 C110 12 136 40 138 76 C142 116 116 146 82 146 C46 146 20 124 22 90 C24 52 42 28 74 20Z", shine: [48,46,12,-38], shadow: 46 },
  fitness: { path: "M42 42 C68 22 108 26 130 44 C150 62 148 112 126 132 C104 152 52 148 30 126 C8 106 14 62 42 42Z", shine: [40,62,13,-30], shadow: 50 },
  chef: { path: "M36 48 C62 32 106 32 130 50 C152 68 144 118 122 136 C100 152 54 148 30 130 C10 114 14 66 36 48Z", shine: [42,68,12,-30], shadow: 49 },
  money: { path: "M80 26 C120 24 140 50 138 86 C138 124 114 148 78 146 C42 144 20 120 22 84 C22 50 46 28 80 26Z", shine: [46,52,13,-35], shadow: 45 },
  music: { path: "M76 28 C108 18 126 46 130 74 C140 110 126 140 94 146 C56 154 32 136 28 104 C20 72 40 40 76 28Z", shine: [50,52,12,-40], shadow: 44 },
  design: { path: "M52 30 C82 10 124 28 140 60 C154 88 134 100 122 108 C112 114 128 136 102 144 C66 156 26 132 20 100 C12 68 26 46 52 30Z", shine: [44,50,13,-40], shadow: 46 },
  sales: { path: "M46 40 C76 20 112 34 134 56 C156 78 142 116 116 134 C86 154 42 146 24 122 C6 98 18 60 46 40Z", shine: [44,58,12,-34], shadow: 48 },
  learning: { path: "M80 24 C106 24 118 46 130 72 C150 112 126 148 80 148 C34 148 12 114 30 76 C42 48 52 24 80 24Z", shine: [50,62,12,-46], shadow: 45 },
  automation: { path: "M32 50 C34 24 64 16 80 32 C100 12 130 28 132 50 C156 60 148 90 138 106 C148 132 116 150 94 138 C72 156 38 146 36 124 C10 116 8 86 26 72 C20 64 24 54 32 50Z", shine: [47,48,12,-32], shadow: 49 },
};

function eyes(x = 55, y = 60, gap = 36, height = 32): string {
  return `<g class="look"><g class="blink"><rect x="${x}" y="${y}" width="14" height="${height}" rx="7" fill="${WHITE}"/><rect x="${x+gap}" y="${y}" width="14" height="${height}" rx="7" fill="${WHITE}"/></g></g>`;
}
const smile = (x = 80, y = 104, width = 12) => `<g class="mouth"><path d="M${x-width} ${y} Q${x} ${y+10} ${x+width} ${y}" fill="none" stroke="${INK}" stroke-width="3" stroke-linecap="round"/></g>`;
const cheeks = (y = 98) => `<ellipse cx="52" cy="${y}" rx="7" ry="4" fill="${CORAL}" opacity=".5"/><ellipse cx="112" cy="${y}" rx="7" ry="4" fill="${CORAL}" opacity=".5"/>`;

type Sign = { back?: string; front?: string; face?: string };
const signs: Record<FaceJob, (fill: string) => Sign> = {
  bot: () => ({ back: `<g class="wobble"><path d="M80 20 V2" stroke="${INK}" stroke-width="3" stroke-linecap="round"/><circle cx="80" cy="-3" r="6" fill="${CORAL}" stroke="${INK}" stroke-width="2.5"/></g>` }),
  planner: () => ({ back: `<g class="wobble"><path d="M68 34 L64 4" stroke="${INK}" stroke-width="3" stroke-linecap="round"/><path d="M64 4 L86 9 L66 17Z" fill="#e8613a" stroke="${INK}" stroke-width="2" stroke-linejoin="round"/></g>`, face: eyes(55,66)+smile(80,108,8) }),
  story: fill => ({ face: eyes(57,62)+`<path d="M50 54 H76 V73 H50Z M86 54 H112 V73 H86Z" fill="${fill}"/><path d="M53 73 H75 M89 73 H111" stroke="${INK}" stroke-width="2.5" stroke-linecap="round"/>`+cheeks()+`<g class="mumble"><circle cx="82" cy="108" r="4" fill="${INK}"/></g>` }),
  code: () => ({ front: `<g class="wobble"><path d="M124 2 L128 13 L139 17 L128 21 L124 32 L120 21 L109 17 L120 13Z" fill="#f2b33d" stroke="${INK}" stroke-width="2" stroke-linejoin="round"/></g>`, face: `<g class="look"><g class="blink"><rect x="72" y="58" width="20" height="44" rx="10" fill="${WHITE}"/></g></g>`+smile(82,116,18) }),
  review: () => ({ face: eyes(51,60,42)+`<g class="monocle"><circle cx="100" cy="76" r="18" fill="none" stroke="${INK}" stroke-width="3.5"/><path d="M114 88 Q128 110 120 136" fill="none" stroke="${INK}" stroke-width="2" stroke-dasharray="2 4" stroke-linecap="round"/></g>`+smile(77,108,11) }),
  inbox: () => ({ face: eyes(58,64,30)+`<g class="talk"><path d="M66 104 Q80 126 94 104Z" fill="${INK}" stroke="${INK}" stroke-width="2" stroke-linejoin="round"/><ellipse cx="80" cy="114" rx="6" ry="3.5" fill="#e8613a"/></g>`, front: `<g class="waves"><path d="M140 72 Q148 86 140 100 M149 64 Q160 86 149 108" fill="none" stroke="${INK}" stroke-width="3" stroke-linecap="round"/></g>` }),
  writer: () => ({ back: `<g class="wobble"><path d="M80 20 C76 4 88 -8 100 -10 C94 0 92 10 84 20Z" fill="${GOLD}" stroke="${INK}" stroke-width="2" stroke-linejoin="round"/></g>`, face: eyes(56,62,34)+cheeks(96)+`<g class="mumble"><path d="M72 102 Q80 109 88 102" fill="none" stroke="#f7f5f2" stroke-width="3" stroke-linecap="round"/></g>` }),
  security: () => ({ face: `<rect x="38" y="74" width="84" height="36" rx="18" fill="${INK}"/>`+eyes(58,80,32,24)+`<g class="mouth"><path d="M70 124 H90" stroke="${INK}" stroke-width="3" stroke-linecap="round"/></g>` }),
  travel: () => ({ back: `<g class="wobble"><path d="M82 -2 L87 13 L105 20 V25 L87 22 L85 33 H78 L76 22 L59 25 V20 L76 13 L78 -2Z" fill="${GOLD}" stroke="${INK}" stroke-width="2" stroke-linejoin="round"/></g>` }),
  data: () => ({ face: eyes(53,57,38)+smile(80,102,9), front: `<g class="chart"><rect x="58" y="122" width="10" height="10" rx="2" fill="${WHITE}"/><rect x="76" y="114" width="10" height="18" rx="2" fill="${WHITE}"/><rect x="94" y="106" width="10" height="26" rx="2" fill="${WHITE}"/></g>` }),
  news: () => ({ back: `<g class="wobble"><path d="M32 48 L78 4 L126 48Z" fill="#f7f5f2" stroke="${INK}" stroke-width="2.5" stroke-linejoin="round"/><path d="M61 28 H93 M54 35 H102" stroke="${INK}" stroke-width="2" stroke-linecap="round"/></g>`, face: eyes(55,70)+smile(80,114,10) }),
  research: () => ({ face: eyes(52,62,36)+smile(79,110,10), front: `<g class="magnify"><path d="M122 104 L142 128" stroke="${INK}" stroke-width="7" stroke-linecap="round"/><circle cx="113" cy="91" r="20" fill="#ffffff" fill-opacity=".14" stroke="${INK}" stroke-width="3.5"/><path d="M101 89 Q103 79 111 79" fill="none" stroke="${WHITE}" stroke-width="3" stroke-linecap="round"/></g>` }),
  fitness: () => ({ front: `<path d="M26 55 Q80 39 136 55 L138 65 Q80 49 23 65Z" fill="#f7f5f2"/><path d="M27 59 Q80 43 136 59" fill="none" stroke="${CORAL}" stroke-width="3"/>`, face: eyes(54,72)+smile(80,116,13) }),
  chef: () => ({ front: `<g class="wobble"><path d="M44 48 V30 C24 16 40 -7 57 3 C63 -15 88 -15 97 2 C118 -7 139 14 119 31 V48Z" fill="#f7f5f2" stroke="${INK}" stroke-width="2.5" stroke-linejoin="round"/><path d="M45 38 H118" stroke="${GOLD}" stroke-width="3"/></g>`, face: eyes(54,70)+smile(80,116,14) }),
  money: () => ({ back: `<path d="M80 30 V12" stroke="${INK}" stroke-width="3"/><g class="coin"><circle cx="80" cy="7" r="14" fill="#f2b33d" stroke="${INK}" stroke-width="2.5"/><path d="M80 -2 V16 M85 1 H77 Q72 1 77 7 L83 8 Q89 14 75 13" fill="none" stroke="${INK}" stroke-width="2" stroke-linecap="round"/></g>` }),
  music: () => ({ back: `<path d="M24 96 V70 C24 11 138 11 138 70 V96" fill="none" stroke="${INK}" stroke-width="8" stroke-linecap="round"/>`, front: `<rect x="17" y="68" width="16" height="39" rx="8" fill="${INK}"/><rect x="129" y="68" width="16" height="39" rx="8" fill="${INK}"/>`, face: eyes(55,66)+smile(80,111,14) }),
  design: () => ({ back: `<g class="wobble"><path d="M110 46 L125 9 L134 13 L120 50Z" fill="${GOLD}" stroke="${INK}" stroke-width="2"/><path d="M125 9 C122 -2 130 -10 139 -10 L134 13Z" fill="${CORAL}" stroke="${INK}" stroke-width="2"/></g>`, face: eyes(52,62,34)+smile(77,110,12) }),
  sales: () => ({ front: `<g class="tag"><path d="M108 106 H135 L148 119 L135 132 H108Z" fill="${GOLD}" stroke="${INK}" stroke-width="2.5" stroke-linejoin="round"/><circle cx="136" cy="119" r="3" fill="${INK}"/></g>`, face: eyes(51,65,35)+smile(76,109,13) }),
  learning: () => ({ front: `<path d="M50 42 V24 H110 V42 Q80 54 50 42Z" fill="${INK}"/><path d="M22 17 L80 -3 L138 17 L80 36Z" fill="${INK}"/><path d="M130 20 V52 L136 62" fill="none" stroke="${GOLD}" stroke-width="3" stroke-linecap="round"/><circle cx="137" cy="64" r="4" fill="${GOLD}"/>`, face: eyes(55,70)+smile(80,116,11) }),
  automation: () => ({ front: `<g class="gear"><path d="M127 18 V7 M127 51 V40 M111 29 H100 M154 29 H143 M115 17 L107 9 M147 49 L139 41 M115 41 L107 49 M147 9 L139 17" stroke="${INK}" stroke-width="5" stroke-linecap="round"/><circle cx="127" cy="29" r="15" fill="${GOLD}" stroke="${INK}" stroke-width="3"/><circle cx="127" cy="29" r="5" fill="${INK}"/></g>`, face: eyes(54,66)+smile(80,112,10) }),
};

/** Markup contains trusted drawings and a validated colour, never stored user HTML. */
export function faceSVG(job: FaceJob, color: string): string {
  const fill = /^#[0-9a-f]{6}$/i.test(color) ? color : "#7a7f8c";
  const h = heads[job], sign = signs[job](fill);
  const [x,y,rx,angle] = h.shine;
  return `<svg viewBox="0 -14 164 180" width="100%" height="100%" aria-hidden="true"><ellipse cx="80" cy="158" rx="${h.shadow}" ry="5" fill="${INK}" opacity=".12"/><g class="body">${sign.back || ""}<path d="${h.path}" fill="${fill}"/><ellipse cx="${x}" cy="${y}" rx="${rx}" ry="${rx/2}" transform="rotate(${angle} ${x} ${y})" fill="${WHITE}" opacity=".3"/>${sign.face ?? eyes()+smile()}${sign.front || ""}</g></svg>`;
}

// Idle faces blink; working ones also look around and move; typing ones
// (the reply is being written) also move their mouth.
export function BotFace({ icon, color, seed, size, working = false, typing = false }: { icon: string; color: string; seed: string; size: number; working?: boolean; typing?: boolean }) {
  const job = faceJob(icon);
  const markup = useMemo(() => faceSVG(job, color), [job, color]);
  const timing = useMemo(() => {
    const rand = seeded(seed || "bot");
    return { "--delay": `${(-Math.abs(rand())*5).toFixed(2)}s`, "--blink": `${(3.5+Math.abs(rand())*2.5).toFixed(2)}s`, "--bob": `${(2.4+Math.abs(rand())*1.2).toFixed(2)}s` } as React.CSSProperties;
  }, [seed]);
  return <span className={`bot-face face-${job} ${working || typing ? "working" : ""} ${typing ? "typing" : ""}`} style={{ width:size, height:size, ...timing }} aria-hidden dangerouslySetInnerHTML={{ __html:markup }} />;
}
