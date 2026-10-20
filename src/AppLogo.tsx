// The app's logo: a blob robot dog in one colour, with no tile or circle
// behind it, from the same family as the bot faces (pill eyes, floppy ears,
// antenna). It takes the text colour, so it is dark in light mode and light in
// dark mode; the gaps (eyes, nose, mouth, ear outlines) take --logo-cut, the
// colour of the surface it sits on. It blinks and twitches its ears now and
// then; while the main assistant works it also bobs, looks around and glows
// its antenna, and while its reply is being written it talks and pokes its
// tongue out (see .app-logo in styles.css). The app icon is the same
// face, cream on ink (assets/icon.svg).

const INK = "currentColor";
const CUT = "var(--logo-cut, var(--bg))";

export function AppLogo({ size = 36, working = false, typing = false }: { size?: number; working?: boolean; typing?: boolean }) {
  return <svg className={`app-logo ${working || typing ? "working" : ""} ${typing ? "typing" : ""}`} width={size} height={size} viewBox="10 6 180 180" aria-hidden>
    <g className="logo-head">
      <g className="logo-antenna">
        <line x1="100" y1="42" x2="100" y2="22" stroke={INK} strokeWidth="4" strokeLinecap="round" />
        <circle className="logo-bulb" cx="100" cy="16" r="7" fill={INK} />
      </g>
      <path d="M100 40 C140 40 166 66 164 108 C162 150 136 174 100 174 C64 174 38 150 36 108 C34 66 60 40 100 40Z" fill={INK} />
      <ellipse className="logo-shine" cx="74" cy="60" rx="14" ry="7" transform="rotate(-32 74 60)" fill="#fff" opacity="0.16" />
      <g className="logo-ear left"><path d="M56 56 C32 50 14 70 18 102 C21 126 36 136 47 126 C56 118 58 92 64 74 Z" fill={INK} stroke={CUT} strokeWidth="4" strokeLinejoin="round" /></g>
      <g className="logo-ear right"><path d="M144 56 C168 50 186 70 182 102 C179 126 164 136 153 126 C144 118 142 92 136 74 Z" fill={INK} stroke={CUT} strokeWidth="4" strokeLinejoin="round" /></g>
      <g className="logo-look">
        <g className="logo-eyes">
          <rect x="72" y="80" width="14" height="32" rx="7" fill={CUT} />
          <rect x="114" y="80" width="14" height="32" rx="7" fill={CUT} />
        </g>
      </g>
      <path d="M88 124 Q100 118 112 124 Q110 136 100 138 Q90 136 88 124Z" fill={CUT} />
      <g className="logo-mouth">
        <line x1="100" y1="138" x2="100" y2="146" stroke={CUT} strokeWidth="3" strokeLinecap="round" />
        <path d="M86 144 Q93 153 100 146 Q107 153 114 144" fill="none" stroke={CUT} strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" />
      </g>
      <path className="logo-tongue" d="M103 149 L113 149 L113 158 Q108 164 103 158 Z" fill={CUT} />
    </g>
  </svg>;
}
