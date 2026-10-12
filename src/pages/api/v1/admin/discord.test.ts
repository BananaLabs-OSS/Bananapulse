import { beforeEach, expect, it, vi } from 'vitest';
const mocks = vi.hoisted(() => ({ auth: vi.fn(), call: vi.fn(), configured: vi.fn() }));
vi.mock('@/lib/admin-api', () => ({ requireAdmin: mocks.auth,
  ok: (data: unknown) => new Response(JSON.stringify({ data })),
  err: (code: string, message: string, status: number) => new Response(JSON.stringify({ error: { code, message } }), { status }),
}));
vi.mock('@/lib/pulp-bridge', async (importOriginal) => ({ ...(await importOriginal<any>()), callPulpEvent: mocks.call, pulpBridgeConfigured: mocks.configured }));
import { PulpBridgeError } from '@/lib/pulp-bridge';
import { GET, POST } from './discord';
const url = new URL('https://status.example.test/api/v1/admin/discord');
function ctx(body?: object, origin = url.origin): any {
  return { url, request: new Request(url, body ? { method: 'POST', headers: { Origin: origin, 'Content-Type': 'application/json' }, body: JSON.stringify(body) } : {}) };
}
beforeEach(() => { vi.resetAllMocks(); mocks.auth.mockResolvedValue('admin@example.test'); mocks.configured.mockReturnValue(true); });
it('requires an admin session before reading or changing settings', async () => {
  mocks.auth.mockResolvedValue(new Response('Unauthorized', { status: 401 }));
  expect((await GET(ctx())).status).toBe(401);
  expect((await POST(ctx({ action: 'test' }))).status).toBe(401);
  expect(mocks.call).not.toHaveBeenCalled();
});
it('rejects cross-origin requests before dispatching a test message', async () => {
  expect((await POST(ctx({ action: 'test' }, 'https://evil.test'))).status).toBe(403);
  expect(mocks.call).not.toHaveBeenCalled();
});
it('returns masked settings with no cache and never requires the saved URL to update preferences', async () => {
  mocks.call.mockResolvedValue({ configured: true, enabled: true, outages: true, recoveries: false });
  const response = await POST(ctx({ action: 'save', webhookUrl: '', remove: false, enabled: true, outages: true, recoveries: false }));
  expect(response.status).toBe(200);
  expect(response.headers.get('Cache-Control')).toBe('no-store');
  expect(await response.json()).toEqual({ data: { configured: true, enabled: true, outages: true, recoveries: false } });
});
it('does not expose host errors that could contain secret URLs', async () => {
  mocks.call.mockRejectedValue(new Error('https://discord.com/api/webhooks/private-secret'));
  const response = await GET(ctx());
  expect(response.status).toBe(503);
  expect(await response.text()).not.toContain('private-secret');
});
it('rejects malformed settings and unconfigured hosts', async () => {
  expect((await POST(ctx({ action: 'save', webhookUrl: '', enabled: 'yes' }))).status).toBe(400);
  mocks.configured.mockReturnValue(false);
  expect((await GET(ctx())).status).toBe(503);
  expect(mocks.call).not.toHaveBeenCalled();
});
it('returns a private API error when authentication owner is unavailable', async () => {
  mocks.auth.mockRejectedValue(new Error('private transport details'));
  for (const response of [await GET(ctx()), await POST(ctx({ action: 'test' }))]) {
    expect(response.status).toBe(503);
    expect(response.headers.get('Cache-Control')).toBe('no-store');
    expect(await response.text()).not.toContain('private transport details');
  }
  expect(mocks.call).not.toHaveBeenCalled();
});
it('accepts only bounded diagnostic metadata after authentication', async () => {
  const log = vi.spyOn(console, 'info').mockImplementation(() => {});
  expect((await POST(ctx({ action: 'diagnostic', operation: 'load', status: 502, requestId: 'abc-123', ray: 'abc-XYZ' }))).status).toBe(200);
  expect((await POST(ctx({ action: 'diagnostic', operation: 'load', status: 502, requestId: 'private-url', ray: '' }))).status).toBe(400);
  expect(mocks.call).not.toHaveBeenCalled();
  log.mockRestore();
});

it('returns actionable validation errors instead of misclassifying them as a gateway outage', async () => {
  mocks.call.mockRejectedValue(new PulpBridgeError('Rejected', 400, JSON.stringify({ error: 'save a webhook before enabling notifications' })));
  const response = await POST(ctx({ action: 'save', webhookUrl: '', enabled: true, outages: true, recoveries: true, remove: false }));
  expect(response.status).toBe(400);
  expect(await response.text()).toContain('Paste a Discord webhook URL before enabling alerts');
});
it('does not return arbitrary private validation payloads', async () => {
  mocks.call.mockRejectedValue(new PulpBridgeError('Rejected', 400, JSON.stringify({ error: 'private-webhook-secret' })));
  const response = await POST(ctx({ action: 'test' }));
  expect(response.status).toBe(400);
  expect(await response.text()).not.toContain('private-webhook-secret');
});

it('canonicalizes copied legacy Discord webhook URLs before host validation', async () => {
  mocks.call.mockResolvedValue({ configured: true, enabled: true });
  await POST(ctx({ action: 'save', webhookUrl: 'https://discordapp.com/api/webhooks/123456789012345678/fake_test_token_long_enough', enabled: true, outages: true, recoveries: true, remove: false }));
  expect(mocks.call.mock.calls[0][1].webhookUrl).toBe('https://discord.com/api/webhooks/123456789012345678/fake_test_token_long_enough');
});
it('keeps deceptive hostnames and credential-bearing URLs for host rejection', async () => {
  mocks.call.mockResolvedValue({});
  for (const webhookUrl of ['https://discordapp.com.attacker.example/api/webhooks/1/token', 'https://user@discordapp.com/api/webhooks/1/token']) {
    await POST(ctx({ action: 'save', webhookUrl, enabled: false, outages: true, recoveries: true, remove: false }));
    expect(mocks.call.mock.calls.at(-1)![1].webhookUrl).toBe(webhookUrl);
  }
});
