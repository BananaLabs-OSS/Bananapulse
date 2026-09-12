import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  getMonitorProjection,
  pulpBridgeConfigured,
  pulpMonitorProjectionConfigured,
  pulpOwnerRouteFamilyConfigured,
  pulpSubscriberLifecycleConfigured,
  resetMonitorProjectionCacheForTests,
} from './pulp-bridge';

const keys = [
  'PULP_BRIDGE_URL',
  'PULP_MONITOR_OWNER_ENABLED',
  'PULP_SUBSCRIBERS_OWNER_ENABLED',
  'PULP_SUBSCRIBER_TOKEN_SECRET',
  'PULP_INCIDENTS_OWNER_ENABLED',
] as const;
const original = Object.fromEntries(keys.map((key) => [key, process.env[key]]));

afterEach(() => {
  resetMonitorProjectionCacheForTests();
  vi.restoreAllMocks();
  vi.useRealTimers();
  for (const key of keys) {
    const value = original[key];
    if (value === undefined) delete process.env[key];
    else process.env[key] = value;
  }
});

function projection(revision: number) {
  return {
    version: 'monitor.v1',
    revision,
    components: [],
    sources: [],
    mappings: [],
    incidents: [],
    incident_updates: [],
    maintenance: [],
  };
}

describe('Pulp monitor projection cache', () => {
  it('coalesces the initial owner read and serves the fresh projection', async () => {
    process.env.PULP_BRIDGE_URL = 'http://127.0.0.1:8788';
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(projection(1)), { status: 200 }),
    );

    const [first, concurrent] = await Promise.all([getMonitorProjection(), getMonitorProjection()]);
    const fresh = await getMonitorProjection();

    expect(first.revision).toBe(1);
    expect(concurrent).toBe(first);
    expect(fresh).toBe(first);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('serves bounded stale data immediately while refreshing in the background', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-09-12T00:00:00Z'));
    process.env.PULP_BRIDGE_URL = 'http://127.0.0.1:8788';
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(new Response(JSON.stringify(projection(1)), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify(projection(2)), { status: 200 }));
    const first = await getMonitorProjection();

    vi.setSystemTime(new Date('2026-09-12T00:00:11Z'));
    const stale = await getMonitorProjection();
    expect(stale).toBe(first);
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    await vi.waitFor(async () => expect((await getMonitorProjection()).revision).toBe(2));
  });

  it('fails closed when the stale window expires and the owner is unavailable', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-09-12T00:00:00Z'));
    process.env.PULP_BRIDGE_URL = 'http://127.0.0.1:8788';
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(new Response(JSON.stringify(projection(1)), { status: 200 }))
      .mockRejectedValueOnce(new Error('owner unavailable'));
    await getMonitorProjection();

    vi.setSystemTime(new Date('2026-09-12T00:01:01Z'));
    await expect(getMonitorProjection()).rejects.toThrow('Pulp bridge request failed');
  });
});

describe('Pulp route-family cutover gates', () => {
  it('never enables a family from a flag alone without a bridge', () => {
    delete process.env.PULP_BRIDGE_URL;
    process.env.PULP_INCIDENTS_OWNER_ENABLED = 'true';
    expect(pulpBridgeConfigured()).toBe(false);
    expect(pulpOwnerRouteFamilyConfigured('incidents')).toBe(false);
  });

  it('enables only the explicitly selected family', () => {
    process.env.PULP_BRIDGE_URL = 'http://127.0.0.1:8788';
    process.env.PULP_INCIDENTS_OWNER_ENABLED = 'true';
    expect(pulpOwnerRouteFamilyConfigured('incidents')).toBe(true);
    expect(pulpOwnerRouteFamilyConfigured('maintenance')).toBe(false);
  });

  it('requires the additional subscriber token secret for public lifecycle cutover', () => {
    process.env.PULP_BRIDGE_URL = 'http://127.0.0.1:8788';
    process.env.PULP_SUBSCRIBERS_OWNER_ENABLED = 'true';
    delete process.env.PULP_SUBSCRIBER_TOKEN_SECRET;
    expect(pulpSubscriberLifecycleConfigured()).toBe(false);
    process.env.PULP_SUBSCRIBER_TOKEN_SECRET = 'test-only-secret';
    expect(pulpSubscriberLifecycleConfigured()).toBe(true);
  });

  it('keeps public monitor reads independently gated', () => {
    process.env.PULP_BRIDGE_URL = 'http://127.0.0.1:8788';
    delete process.env.PULP_MONITOR_OWNER_ENABLED;
    expect(pulpMonitorProjectionConfigured()).toBe(false);
    process.env.PULP_MONITOR_OWNER_ENABLED = 'true';
    expect(pulpMonitorProjectionConfigured()).toBe(true);
  });
});
