import { describe, it, expect, vi, beforeEach } from 'vitest';

// AUDIT REGRESSION (MASTER H1 / I3 fail-closed): status.json must NEVER
// synthesize 'operational' on a DB/derivation error. On any failure it emits
// status:'unknown' + live:false with HTTP 503, so badges/monitors treat us as
// unverifiable rather than green.
//
// We mock the data-layer modules status.json.ts imports so we can force the
// success path and the error path deterministically (no DB).

vi.mock('../../lib/components', () => ({
  getPublicLeafComponents: vi.fn(),
  buildSummaryTree: vi.fn(),
}));
vi.mock('../../lib/db-incidents', () => ({ getActiveIncidents: vi.fn(async () => []) }));
vi.mock('../../lib/db-maintenance', () => ({ getUpcomingMaintenance: vi.fn(async () => []) }));
vi.mock('../../lib/pulp-bridge', () => ({
  pulpMonitorProjectionConfigured: vi.fn(() => false),
  getMonitorProjection: vi.fn(),
}));
vi.mock('../../lib/pulp-monitor-projection', () => ({ buildPulpStatusJSON: vi.fn() }));

import { getPublicLeafComponents, buildSummaryTree } from '../../lib/components';
import { getMonitorProjection, pulpMonitorProjectionConfigured } from '../../lib/pulp-bridge';
import { buildPulpStatusJSON } from '../../lib/pulp-monitor-projection';
import { GET } from './status.json';

const mockLeaves = vi.mocked(getPublicLeafComponents);
const mockTree = vi.mocked(buildSummaryTree);

function ctx() {
  return { locals: { scope: null }, url: new URL('http://s.local/api/status.json') } as any;
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(pulpMonitorProjectionConfigured).mockReturnValue(false);
});

describe('GET /api/status.json fail-closed (audit H1 regression)', () => {
  it('success path: 200 with derived overall status', async () => {
    mockLeaves.mockResolvedValue([{ id: 'svc', name: 'Svc', product: 'p', status: 'operational' }] as any);
    mockTree.mockResolvedValue({ status: 'operational' } as any);
    const res = await GET(ctx());
    expect(res.status).toBe(200);
    const body = await res.json();
    expect(body.status).toBe('operational');
    expect(body.services).toHaveLength(1);
  });

  it('REGRESSION: derivation throwing => 503 + status:unknown + live:false (never operational)', async () => {
    mockLeaves.mockRejectedValue(new Error('db down'));
    const res = await GET(ctx());
    expect(res.status).toBe(503);
    const body = await res.json();
    expect(body.status).toBe('unknown');
    expect(body.state).toBe('unknown');
    expect(body.live).toBe(false);
    expect(body.status).not.toBe('operational');
    expect(body.services).toEqual([]);
  });

  it('REGRESSION: buildSummaryTree throwing also fails closed (503/unknown)', async () => {
    mockLeaves.mockResolvedValue([] as any);
    mockTree.mockRejectedValue(new Error('boom'));
    const res = await GET(ctx());
    expect(res.status).toBe(503);
    const body = await res.json();
    expect(body.status).toBe('unknown');
    expect(body.live).toBe(false);
  });

  it('REGRESSION: a missing scope root fails closed instead of treating an empty tree as operational', async () => {
    mockLeaves.mockResolvedValue([] as any);
    mockTree.mockResolvedValue(null);
    const res = await GET(ctx());
    expect(res.status).toBe(503);
    const body = await res.json();
    expect(body.status).toBe('unknown');
    expect(body.live).toBe(false);
  });

  it('uses the composed owner projection when the Pulp bridge is configured', async () => {
    vi.mocked(pulpMonitorProjectionConfigured).mockReturnValue(true);
    vi.mocked(getMonitorProjection).mockResolvedValue({ version: 'monitor.v1' } as any);
    vi.mocked(buildPulpStatusJSON).mockReturnValue({
      status: 'degraded',
      state: 'degraded',
      services: [],
      activeIncidents: [],
      scheduledMaintenance: [],
    });
    const res = await GET(ctx());
    expect(res.status).toBe(200);
    expect((await res.json()).status).toBe('degraded');
    expect(getPublicLeafComponents).not.toHaveBeenCalled();
  });
});
