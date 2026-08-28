import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  callPulpEvent: vi.fn(),
  ownerConfigured: vi.fn(),
  resolveSourceByToken: vi.fn(),
  resolveTarget: vi.fn(),
  appendObservation: vi.fn(),
  snapshotComponent: vi.fn(),
  notifyForComponent: vi.fn(),
  BridgeError: class BridgeError extends Error {
    constructor(
      message: string,
      readonly status: number,
      readonly detail = '',
    ) {
      super(message);
    }
  },
}));

vi.mock('@/lib/pulp-bridge', () => ({
  callPulpEvent: mocks.callPulpEvent,
  PulpBridgeError: mocks.BridgeError,
  pulpOwnerRequestID: (prefix: string) => `${prefix}_stable`,
  pulpOwnerRouteFamilyConfigured: mocks.ownerConfigured,
}));
vi.mock('../../../lib/sources', () => ({
  resolveSourceByToken: mocks.resolveSourceByToken,
  resolveTarget: mocks.resolveTarget,
}));
vi.mock('../../../lib/quorum', () => ({ appendObservation: mocks.appendObservation }));
vi.mock('../../../lib/notify', () => ({
  snapshotComponent: mocks.snapshotComponent,
  notifyForComponent: mocks.notifyForComponent,
}));

import { POST } from './ingest';

function context(body: unknown, token = 'source-token') {
  return {
    request: new Request('https://status.example/api/v1/ingest', {
      method: 'POST',
      headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    }),
  } as Parameters<typeof POST>[0];
}

describe('POST /api/v1/ingest Pulp-owner compatibility', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.ownerConfigured.mockReturnValue(true);
  });

  it('validates the bearer token privately, then sends one Lua-routed owner command', async () => {
    mocks.callPulpEvent
      .mockResolvedValueOnce({ valid: true, source_id: 'source-a' })
      .mockResolvedValueOnce({
        command_id: 'ingest-command',
        evaluation: {
          observation_id: 'observation-a', component_id: 'component-a', state: 'declared', level: 'major',
          non_ok: 2, sources: 3, reduced_coverage: false,
        },
      });

    const response = await POST(context({ target: 'payments', signal: 'down', detail: 'probe failed' }));

    expect(response.status).toBe(202);
    await expect(response.json()).resolves.toEqual({
      data: {
        observation_id: 'observation-a', component_id: 'component-a', state: 'declared', level: 'major',
        non_ok: 2, sources: 3, reduced_coverage: false,
      },
    });
    expect(mocks.callPulpEvent).toHaveBeenNthCalledWith(1,
      'bananapulse.host.auth.source-credential.validate.v1', expect.objectContaining({ token: 'source-token' }));
    expect(mocks.callPulpEvent).toHaveBeenNthCalledWith(2,
      'bananapulse.monitor.ingest.authenticated.v1', expect.objectContaining({
        kind: 'ingest_observation',
        ingest: expect.objectContaining({ source_id: 'source-a', raw_label: 'payments', signal: 'down' }),
      }));
    expect(mocks.resolveSourceByToken).not.toHaveBeenCalled();
    expect(mocks.appendObservation).not.toHaveBeenCalled();
    expect(mocks.notifyForComponent).not.toHaveBeenCalled();
  });

  it('keeps the legacy implementation when either owner family is not enabled', async () => {
    mocks.ownerConfigured.mockImplementation((family: string) => family === 'ingest');
    mocks.resolveSourceByToken.mockResolvedValue({ id: 'legacy-source', defaultTtl: null });
    mocks.resolveTarget.mockResolvedValue('component-a');
    mocks.snapshotComponent.mockResolvedValue({ incidentId: null, status: null, severity: null });
    mocks.appendObservation.mockResolvedValue({
      observationId: 'legacy-observation',
      evaluation: { state: 'ok', level: null, nonOkCount: 0, totalSources: 1, reducedCoverage: false },
    });

    const response = await POST(context({ target: 'payments', signal: 'ok' }));

    expect(response.status).toBe(202);
    expect(mocks.callPulpEvent).not.toHaveBeenCalled();
    expect(mocks.resolveSourceByToken).toHaveBeenCalledWith('source-token');
    expect(mocks.appendObservation).toHaveBeenCalledOnce();
  });

  it('does not fall back after an attempted owner mutation', async () => {
    mocks.callPulpEvent
      .mockResolvedValueOnce({ valid: true, source_id: 'source-a' })
      .mockRejectedValueOnce(new mocks.BridgeError('unmapped', 422, JSON.stringify({
        error: { code: 'unmapped_target', message: 'No mapping for target "payments" on this source. Add a source_target_map row.' },
      })));

    const response = await POST(context({ target: 'payments', signal: 'down' }));

    expect(response.status).toBe(422);
    await expect(response.json()).resolves.toEqual({
      error: {
        code: 'unmapped_target',
        message: 'No mapping for target "payments" on this source. Add a source_target_map row.',
      },
    });
    expect(mocks.resolveSourceByToken).not.toHaveBeenCalled();
    expect(mocks.appendObservation).not.toHaveBeenCalled();
  });
});
