import { useEffect, useRef, useState } from "react";
import { Globe, LoaderCircle, X } from "lucide-react";
import { api } from "./api";
import type { Bot } from "./types";

type Live = { active: boolean; ws_url?: string; held: boolean; backend?: string };

// BrowserView shows the bot's browser live. agent-browser streams each
// session's screen as JPEG frames over a loopback WebSocket, about 20 a
// second; the panel draws at most a few of them and skips the rest, and it
// disconnects while Rex's window is hidden so the bot's browser stops
// encoding frames nobody sees. It is view-only (use "Take over browser" to
// control it).

const FRAME_INTERVAL_MS = 200;

function useDocumentVisible() {
  const [visible, setVisible] = useState(document.visibilityState === "visible");
  useEffect(() => {
    const update = () => setVisible(document.visibilityState === "visible");
    document.addEventListener("visibilitychange", update);
    return () => document.removeEventListener("visibilitychange", update);
  }, []);
  return visible;
}

function jpegURL(base64: string): string {
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index++) bytes[index] = binary.charCodeAt(index);
  return URL.createObjectURL(new Blob([bytes], { type: "image/jpeg" }));
}
export function BrowserView({ bot, onClose }: { bot: Bot; onClose: () => void }) {
  const [live, setLive] = useState<Live | null>(null);
  const [hasFrame, setHasFrame] = useState(false);
  const latestFrame = useRef("");
  const imageRef = useRef<HTMLImageElement>(null);
  const visible = useDocumentVisible();
  const [page, setPage] = useState<{ url: string; title: string }>({ url: "", title: "" });
  const [connected, setConnected] = useState(false);
  const socketRef = useRef<WebSocket | null>(null);

  // Find the bot's browser; keep looking until it opens.
  useEffect(() => {
    let cancelled = false;
    let timer = 0;
    const poll = async () => {
      try {
        const next = await api.browserLive(bot.id);
        if (cancelled) return;
        setLive(current => current?.ws_url === next.ws_url && current?.held === next.held && current?.active === next.active && current?.backend === next.backend ? current : next);
      } catch { /* retry */ }
      if (!cancelled) timer = window.setTimeout(poll, 3000);
    };
    void poll();
    return () => { cancelled = true; window.clearTimeout(timer); };
  }, [bot.id]);

  useEffect(() => {
    if (!live?.active || !live.ws_url || !visible) return;
    const socket = new WebSocket(live.ws_url);
    socketRef.current = socket;
    socket.onopen = () => setConnected(true);
    socket.onclose = () => setConnected(false);
    socket.onmessage = event => {
      if (typeof event.data !== "string") return;
      // Frames are most of the traffic: keep the newest one as is and only
      // take it apart when it is drawn.
      if (event.data.startsWith('{"data":"') && event.data.endsWith('"type":"frame"}')) {
        latestFrame.current = event.data;
        return;
      }
      try {
        const message = JSON.parse(event.data) as { type: string; tabs?: { active: boolean; url: string; title: string }[] };
        if (message.type === "tabs") {
          const active = message.tabs?.find(tab => tab.active) || message.tabs?.[0];
          if (active) setPage({ url: active.url, title: active.title });
        }
      } catch { /* ignore malformed messages */ }
    };
    // Draw the newest frame a few times a second, outside React.
    let shownURL = "";
    const timer = window.setInterval(() => {
      const raw = latestFrame.current;
      const image = imageRef.current;
      if (!raw) return;
      latestFrame.current = "";
      setHasFrame(true);
      if (!image) return;
      const url = jpegURL(raw.slice('{"data":"'.length, raw.indexOf('"', '{"data":"'.length)));
      const previous = shownURL;
      shownURL = url;
      image.src = url;
      if (previous) URL.revokeObjectURL(previous);
    }, FRAME_INTERVAL_MS);
    return () => {
      window.clearInterval(timer);
      socket.close();
      socketRef.current = null;
      if (shownURL) URL.revokeObjectURL(shownURL);
    };
  }, [live?.active, live?.ws_url, visible]);

  return <aside className="drawer browser-drawer">
    <header className="drawer-header">
      <div className="browser-title"><small className="eyebrow">{live?.held ? "You are in control" : "Live browser"}</small><h3 title={page.url}>{page.title || page.url || `${bot.name}'s browser`}</h3></div>
      <button type="button" className="icon-button ghost" onClick={onClose} aria-label="Close browser view"><X size={16} /></button>
    </header>
    {page.url && <div className="browser-url"><Globe size={12} /><span>{page.url}</span>{connected && <span className="live-dot" title="Live" />}</div>}
    <div className="browser-screen">
      <img ref={imageRef} alt={`${bot.name}'s browser`} hidden={!hasFrame || live?.held} />
      {(!hasFrame || live?.held) && <div className="browser-empty">{live?.held ? <>You're using {bot.name}'s browser in its own window. Hand it back when you're done.</> : live?.backend === "extension" ? <>Extension mode opens in the bot’s own browser window. Watch it there; take over when the bot asks for help.</> : live && !live.active ? <>The browser opens when {bot.name} starts browsing.</> : <><LoaderCircle className="spin" size={16} /> Connecting…</>}</div>}
    </div>
    <p className="muted small browser-note">View only. To sign in or click yourself, use “Take over browser” when the bot asks for help.</p>
  </aside>;
}
