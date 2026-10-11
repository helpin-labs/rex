// @vitest-environment jsdom
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const source = readFileSync('internal/rex/browser_extension/content.js', 'utf8');
function page() {
  document.body.innerHTML = '<label>Name<input id="name"></label><button id="save">Save</button><input type="file"><div id="captcha"><button>Verify</button></div>';
  let handler: (message: unknown, sender: unknown, reply: (value: unknown) => void) => unknown;
  Object.defineProperty(window.HTMLElement.prototype, 'getClientRects', { configurable: true, value: () => [{ width: 100, height: 20 }] });
  window.HTMLElement.prototype.scrollIntoView = () => {};
  Object.defineProperty(window.HTMLElement.prototype, 'innerText', { configurable: true, get() { return this.textContent; } });
  Object.assign(window, { chrome: { runtime: { id: 'rex', onMessage: { addListener: (callback: typeof handler) => { handler = callback; } } } } });
  window.eval(source);
  const call = (args: string[], expires = Date.now() + 5000) => new Promise<{ success: boolean; error?: string; data: { snapshot?: string } }>(resolve => handler({ rex: true, args, expires }, { id: 'rex' }, resolve as (value: unknown) => void));
  const ref = (snapshot: string, name: string) => '@' + snapshot.split('\n').find(line => line.includes(name))!.match(/ref=([^\]]+)/)![1];
  return { window, call, ref };
}

describe('extension DOM driver', () => {
  it('fills normal forms with synthetic input and invalidates old snapshots', async () => {
    const { window, call, ref } = page();
    const snapshot = (await call(['snapshot'])).data.snapshot!;
    const name = ref(snapshot, 'Name');
    let trusted: boolean | undefined;
    window.document.querySelector('#name')!.addEventListener('input', event => { trusted = event.isTrusted; });
    expect((await call(['fill', name, 'Ada'])).success).toBe(true);
    expect((window.document.querySelector('#name') as HTMLInputElement).value).toBe('Ada');
    expect(trusted).toBe(false);
    await call(['snapshot']);
    expect((await call(['fill', name, 'old'])).error).toContain('Stale');
    const current = ref((await call(['snapshot'])).data.snapshot!, 'Name');
    await call(['reset']);
    expect((await call(['click', current])).error).toContain('Stale');
  });

  it('requires a human for verification and uploads and rejects expired actions', async () => {
    const { call, ref } = page();
    const snapshot = (await call(['snapshot'])).data.snapshot!;
    expect((await call(['click', ref(snapshot, 'Verify')])).error).toContain('human takeover');
    expect((await call(['click', ref(snapshot, 'input ""')])).error).toContain('File uploads');
    expect((await call(['click', ref(snapshot, 'Save')], Date.now() - 1)).error).toContain('Expired');
    expect((await call(['press', 'Enter'])).success).toBe(false);
  });
});
