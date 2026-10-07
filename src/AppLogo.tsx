// The app's logo: a simple robot dog face in one colour, with no tile or
// circle behind it. It takes the text colour, so it is dark in light mode and
// light in dark mode; the gaps (eyes, nose, outlines) take --logo-cut, the
// colour of the surface it sits on. It blinks now and then; while the main
// assistant works, its ears flap, its antenna glows and it looks around (see
// .app-logo in styles.css). The app icon is the same face, white on black
// (assets/icon.svg).

const INK = "currentColor";
const CUT = "var(--logo-cut, var(--bg))";

export function AppLogo({ size = 36, working = false }: { size?: number; working?: boolean }) {
  return <svg className={`app-logo ${working ? "working" : ""}`} width={size} height={size} viewBox="70 48 260 260" aria-hidden>
    <g className="logo-head">
      <g className="logo-antenna">
        <line x1="200" y1="112" x2="200" y2="82" stroke={INK} strokeWidth="8" strokeLinecap="round" />
        <circle className="logo-bulb" cx="200" cy="74" r="11" fill={INK} />
      </g>
      <rect x="130" y="110" width="140" height="150" rx="50" fill={INK} />
      <path className="logo-ear left" d="M142 116 C110 110 86 130 84 170 C82 212 92 240 108 246 C124 250 132 236 134 212 L136 138 Z" fill={INK} stroke={CUT} strokeWidth="10" strokeLinejoin="round" />
      <path className="logo-ear right" d="M258 116 C290 110 314 130 316 170 C318 212 308 240 292 246 C276 250 268 236 266 212 L264 138 Z" fill={INK} stroke={CUT} strokeWidth="10" strokeLinejoin="round" />
      <g className="logo-eyes">
        <rect x="162" y="160" width="20" height="30" rx="10" fill={CUT} />
        <rect x="218" y="160" width="20" height="30" rx="10" fill={CUT} />
      </g>
      <g className="logo-muzzle">
        <rect x="148" y="210" width="104" height="80" rx="40" fill={INK} stroke={CUT} strokeWidth="10" />
        <rect x="180" y="220" width="40" height="24" rx="12" fill={CUT} />
        <path d="M200 244 L200 258 M178 262 C188 272 200 268 200 258 C200 268 212 272 222 262" fill="none" stroke={CUT} strokeWidth="7" strokeLinecap="round" />
      </g>
    </g>
  </svg>;
}
