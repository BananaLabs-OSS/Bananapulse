import { afterEach, expect, it, vi } from 'vitest';
import { discordAdminRequest } from './discord-admin-request';
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });
it('recovers a transient HTML 502 on reads and reports only bounded metadata', async () => {
  vi.useFakeTimers();
  const fetcher = vi.fn().mockImplementation((_url, options) => {
    if (options.method === 'POST') return Promise.resolve(Response.json({ data: { recorded: true } }));
    return Promise.resolve(fetcher.mock.calls.filter(([,o]) => o.method === 'GET').length === 1
      ? new Response('<!DOCTYPE html>private proxy payload', { status: 502, headers: { 'Content-Type': 'text/html', 'cf-ray': 'abc-XYZ' } })
      : Response.json({ data: { configured: false } }));
  });
  vi.stubGlobal('fetch', fetcher);
  const pending = discordAdminRequest();
  await vi.runAllTimersAsync();
  expect(await pending).toEqual({ configured: false });
  const report = JSON.parse(fetcher.mock.calls.find(([,o]) => o.method === 'POST')![1].body);
  expect(report).toMatchObject({ action: 'diagnostic', operation: 'load', status: 502, ray: 'abc-XYZ' });
  expect(JSON.stringify(report)).not.toContain('private proxy payload');
});
it('never repeats a save or test after an ambiguous failure and excludes secrets from the report', async () => {
  const fetcher = vi.fn().mockResolvedValue(new Response('<!DOCTYPE html>', { status: 502, headers: { 'Content-Type': 'text/html' } }));
  vi.stubGlobal('fetch', fetcher);
  await expect(discordAdminRequest({ action: 'save', webhookUrl: 'private-webhook-secret' })).rejects.toThrow('Saving settings');
  const bodies = fetcher.mock.calls.map(([,o]) => JSON.parse(o.body));
  expect(bodies.filter(b => b.action === 'save')).toHaveLength(1);
  expect(JSON.stringify(bodies.find(b => b.action === 'diagnostic'))).not.toContain('private-webhook-secret');
});
