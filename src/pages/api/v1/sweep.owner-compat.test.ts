import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  callPulpEvent: vi.fn(),
  configured: vi.fn(),
  sweepQuorum: vi.fn(),
  snapshotAllOpenIncidents: vi.fn(),
  notifyForComponent: vi.fn(),
  BridgeError: class BridgeError extends Error {
    constructor(message: string, readonly status: number, readonly detail = '') { super(message); }
  },
}));

vi.mock('@/lib/pulp-bridge', () => ({
  callPulpEvent: mocks.callPulpEvent,
  PulpBridgeError: mocks.BridgeError,
  pulpOwnerRouteFamilyConfigured: mocks.configured,
}));
vi.mock('@/lib/quorum', () => ({ sweepQuorum: mocks.sweepQuorum }));
vi.mock('@/lib/notify', () => ({
  snapshotAllOpenIncidents: mocks.snapshotAllOpenIncidents,
  notifyForComponent: mocks.notifyForComponent,
}));

import { POST } from './sweep';

function context(secret = 'timer-secret') {
  return {
    request: new Request('https://status.example/api/v1/sweep', {
      method: 'POST', headers: { 'X-Uptime-Hook-Secret': secret },
    }),
  } as Parameters<typeof POST>[0];
}

describe('POST /api/v1/sweep Pulp-owner compatibility', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    process.env.UPTIME_HOOK_SECRET = 'timer-secret';
  });

  it('uses the Lua sweep event and never runs the legacy notifier when gated', async () => {
    mocks.configured.mockReturnValue(true);
    mocks.callPulpEvent.mockResolvedValue({
      sweep: { components: 4, declared: 1, watch: 2, reduced_coverage: 3 },
    });

    const response = await POST(context());

    expect(response.status).toBe(200);
    await expect(response.json()).resolves.toEqual({
      data: { components: 4, declared: 1, watch: 2, reducedCoverage: 3 },
    });
    expect(mocks.callPulpEvent).toHaveBeenCalledWith('bananapulse.monitor.sweep.v1', expect.objectContaining({
      version: 'monitor.v1', kind: 'sweep_reconcile',
    }));
    expect(mocks.sweepQuorum).not.toHaveBeenCalled();
    expect(mocks.notifyForComponent).not.toHaveBeenCalled();
  });

  it('retains the legacy sweep only while its owner gate is disabled', async () => {
    mocks.configured.mockReturnValue(false);
    mocks.snapshotAllOpenIncidents.mockResolvedValue(new Map());
    mocks.sweepQuorum.mockResolvedValue([
      { componentId: 'a', state: 'declared', reducedCoverage: true },
      { componentId: 'b', state: 'watch', reducedCoverage: false },
    ]);

    const response = await POST(context());

    expect(response.status).toBe(200);
    expect(mocks.callPulpEvent).not.toHaveBeenCalled();
    expect(mocks.sweepQuorum).toHaveBeenCalledOnce();
    expect(mocks.notifyForComponent).toHaveBeenCalledTimes(2);
  });

  it('does not fall back after a failed owner sweep', async () => {
    mocks.configured.mockReturnValue(true);
    mocks.callPulpEvent.mockRejectedValue(new mocks.BridgeError('failed', 503));

    const response = await POST(context());

    expect(response.status).toBe(503);
    expect(mocks.sweepQuorum).not.toHaveBeenCalled();
  });
});
