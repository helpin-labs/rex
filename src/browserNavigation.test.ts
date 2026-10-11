import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { describe, expect, it, vi } from 'vitest';

const source = readFileSync('internal/rex/browser_extension/background.js', 'utf8').split("chrome.alarms.create('rex-bridge'")[0];
const missing = () => new Error('Could not establish connection. Receiving end does not exist.');
const ready = { success: true, data: { ready: true } };
function driver(sendMessage: ReturnType<typeof vi.fn>) {
  let now = 0;
  return runInNewContext(source + '\npageCommand', {
    importScripts() {}, self: { REX_BRIDGE: {} }, chrome: { tabs: { sendMessage } },
    Date: { now: () => now }, setTimeout(callback: () => void, ms: number) { now += ms; callback(); },
  }) as (tab: number, command: { args: string[]; expires: number }) => Promise<unknown>;
}

describe('extension navigation recovery', () => {
  it('waits for the new content script and retries a read interrupted by navigation', async () => {
    const send = vi.fn().mockRejectedValueOnce(missing()).mockResolvedValueOnce(ready)
      .mockRejectedValueOnce(missing()).mockResolvedValueOnce(ready)
      .mockResolvedValueOnce({ success: true, data: { text: 'Listing' } });
    expect(await driver(send)(1, { args: ['get', 'text', 'body'], expires: 10000 })).toEqual({ text: 'Listing' });
    expect(send.mock.calls.map(call => call[1].args[0])).toEqual(['ready', 'ready', 'get', 'ready', 'get']);
  });

  it('retries snapshots but never repeats an uncertain click', async () => {
    const snapshot = vi.fn().mockResolvedValueOnce(ready).mockRejectedValueOnce(missing())
      .mockResolvedValueOnce(ready).mockResolvedValueOnce({ success: true, data: { snapshot: 'Fresh refs' } });
    expect(await driver(snapshot)(1, { args: ['snapshot'], expires: 10000 })).toEqual({ snapshot: 'Fresh refs' });
    const click = vi.fn().mockResolvedValueOnce(ready).mockRejectedValueOnce(missing());
    await expect(driver(click)(1, { args: ['click', '@old'], expires: 10000 })).rejects.toThrow('do not repeat it automatically');
    expect(click.mock.calls.map(call => call[1].args[0])).toEqual(['ready', 'click']);
  });

  it('bounds readiness waits and does not retry page validation errors', async () => {
    const send = vi.fn().mockRejectedValue(missing());
    await expect(driver(send)(1, { args: ['snapshot'], expires: 300 })).rejects.toThrow('extension is connected');
    expect(send).toHaveBeenCalledTimes(3);
    const stale = vi.fn().mockResolvedValueOnce(ready).mockResolvedValueOnce({ success: false, error: 'Stale element reference' });
    await expect(driver(stale)(1, { args: ['click', '@old'], expires: 10000 })).rejects.toThrow('Stale');
    expect(stale).toHaveBeenCalledTimes(2);
  });
});
