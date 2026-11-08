import { useEffect, useRef, useState } from "react";
import { Modal } from "./ui";

// The desktop webview shows no window.confirm dialog: it returns false at
// once, so a confirmation there silently cancels the action. confirm() asks
// in Rex's own dialog instead, from anywhere, and resolves with the answer.

type Options = { title?: string; confirmLabel?: string; cancelLabel?: string; danger?: boolean };
type Request = Required<Omit<Options, "title">> & { title: string; message: string; resolve: (ok: boolean) => void };

let show: ((request: Request) => void) | null = null;

export function confirm(message: string, options: Options = {}): Promise<boolean> {
  return new Promise(resolve => {
    const request = { message, resolve, title: options.title ?? "Are you sure?", confirmLabel: options.confirmLabel ?? "OK", cancelLabel: options.cancelLabel ?? "Cancel", danger: options.danger ?? false };
    if (show) show(request); else resolve(false);
  });
}

/** Renders the confirmation dialog; mount it once at the app root. */
export function ConfirmHost() {
  const [request, setRequest] = useState<Request | null>(null);
  const confirmButton = useRef<HTMLButtonElement>(null);
  const pending = useRef<Request | null>(null);
  useEffect(() => {
    // A newer question replaces an unanswered one, which counts as No.
    show = next => { pending.current?.resolve(false); pending.current = next; setRequest(next); };
    return () => { show = null; };
  }, []);
  useEffect(() => {
    if (!request) return;
    confirmButton.current?.focus();
    // Escape answers this dialog only; the editor modal underneath must not
    // also hear it and ask again.
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.stopImmediatePropagation();
      request.resolve(false); pending.current = null; setRequest(null);
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [request]);
  if (!request) return null;
  const answer = (ok: boolean) => { request.resolve(ok); pending.current = null; setRequest(null); };
  return <Modal title={request.title} onClose={() => answer(false)} className="confirm-modal" footer={<>
    <span className="spacer" />
    <button type="button" className="button secondary" onClick={() => answer(false)}>{request.cancelLabel}</button>
    <button type="button" ref={confirmButton} className={`button ${request.danger ? "danger" : "primary"}`} onClick={() => answer(true)}>{request.confirmLabel}</button>
  </>}>
    <p className="confirm-message">{request.message}</p>
  </Modal>;
}
