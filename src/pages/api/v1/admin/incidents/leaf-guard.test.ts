import { describe, it, expect, vi, beforeEach } from 'vitest';

// AUDIT REGRESSION (leaf-only / componentExists write boundary): a manual
// DECLARE must reject any affected id that (a) does not resolve to a real
// component, or (b) is not a LEAF (service/host). Declaring on a non-leaf
// (organization/product) — whose status is DERIVED, not observed — or on an
// unknown id, would create an invisible / mis-placed outage. This pins the
// guard at the admin declare boundary.

const { recordManualOverride, owner } = vi.hoisted(() => ({
  // one-arg signature so .mock.calls[0][0] typechecks (it's called with an opts object)
  recordManualOverride: vi.fn(async (_opts: any) => undefined),
  owner: {
    configured: vi.fn(() => false),
    projection: vi.fn(),
    publish: vi.fn(async () => undefined),
    incident: vi.fn(),
  },
}));

vi.mock('@/lib/admin-api', () => ({
  requireAdmin: vi.fn(async () => 'admin@example.com'),
  ok: (data: unknown, status = 200) =>
    new Response(JSON.stringify({ data }), { status }),
  err: (code: string, message: string, status: number) =>
    new Response(JSON.stringify({ error: { code, message } }), { status }),
}));
vi.mock('@/lib/components', () => ({
  componentExists: vi.fn(),
  isLeafComponent: vi.fn(),
}));
vi.mock('@/lib/sources', () => ({ getManualSource: vi.fn(async () => ({ id: 'manual' })) }));
vi.mock('@/lib/notify', () => ({
  snapshotComponent: vi.fn(async () => ({})),
  notifyForComponent: vi.fn(async () => undefined),
}));
vi.mock('@/lib/quorum', () => ({
  recordManualOverride,
  openIncidentFor: vi.fn(async () => ({ id: 'inc1' })),
}));
vi.mock('@/lib/pulp-bridge', () => ({
  pulpOwnerRouteFamilyConfigured: owner.configured,
}));
vi.mock('../components/pulp-owner', () => ({
  monitorOwnerProjection: owner.projection,
}));
vi.mock('../../incidents/owner', () => ({
  incidentProjection: vi.fn(),
  legacyIncident: vi.fn((incident) => incident),
  newOwnerCommand: vi.fn((_kind, _identity, payload) => ({ id: 'owner-command', ...payload })),
  ownerFailure: vi.fn(() => new Response(null, { status: 503 })),
  ownerIncident: owner.incident,
  publishIncidentCommand: owner.publish,
}));
vi.mock('@/db', () => ({
  db: { select: () => ({ from: () => ({ orderBy: () => ({ limit: async () => [] }) }) }) },
}));
vi.mock('@/db/schema', () => ({ incidents: {} }));

import { componentExists, isLeafComponent } from '@/lib/components';
import { POST } from './index';

const mockExists = vi.mocked(componentExists);
const mockLeaf = vi.mocked(isLeafComponent);

function ctx(body: any) {
  return {
    request: { json: async () => body },
    locals: {},
    cookies: { get: () => undefined },
  } as any;
}

beforeEach(() => {
  vi.clearAllMocks();
  owner.configured.mockReturnValue(false);
});

describe('POST /api/v1/admin/incidents — leaf-only declare guard (audit regression)', () => {
  it('rejects an UNKNOWN component id with 400 (componentExists=false)', async () => {
    mockExists.mockResolvedValue(false);
    mockLeaf.mockResolvedValue(true);
    const res = await POST(ctx({ componentId: 'ghost', severity: 'major' }));
    expect(res.status).toBe(400);
    const body = await res.json();
    expect(body.error.message).toContain('Unknown component');
    expect(recordManualOverride).not.toHaveBeenCalled();
  });

  it('REGRESSION: rejects declaring on a NON-LEAF (org/product) with 400 (isLeafComponent=false)', async () => {
    mockExists.mockResolvedValue(true);
    mockLeaf.mockResolvedValue(false);
    const res = await POST(ctx({ componentId: 'org-root', severity: 'major' }));
    expect(res.status).toBe(400);
    const body = await res.json();
    expect(body.error.message).toContain('not a leaf');
    // No write happened — the guard short-circuits before the engine.
    expect(recordManualOverride).not.toHaveBeenCalled();
  });

  it('requires at least one affected component (empty affects => 400)', async () => {
    const res = await POST(ctx({ severity: 'major' }));
    expect(res.status).toBe(400);
    expect(recordManualOverride).not.toHaveBeenCalled();
  });

  it('a valid leaf declare reaches the engine (recordManualOverride called) and returns 201', async () => {
    mockExists.mockResolvedValue(true);
    mockLeaf.mockResolvedValue(true);
    const res = await POST(ctx({ componentId: 'sessions-api', severity: 'major', title: 'down' }));
    expect(res.status).toBe(201);
    expect(recordManualOverride).toHaveBeenCalledTimes(1);
    expect(recordManualOverride.mock.calls[0][0]).toMatchObject({ componentId: 'sessions-api', signal: 'down' });
  });

  it('rejects the WHOLE multi-affects declare if ANY id is a non-leaf', async () => {
    mockExists.mockResolvedValue(true);
    // first id leaf, second id non-leaf
    mockLeaf.mockResolvedValueOnce(true).mockResolvedValueOnce(false);
    const res = await POST(ctx({ affects: ['svc-a', 'product-b'], severity: 'minor' }));
    expect(res.status).toBe(400);
    expect(recordManualOverride).not.toHaveBeenCalled();
  });

  it('uses owner projection leaf validation without touching legacy component queries', async () => {
    owner.configured.mockReturnValue(true);
    owner.projection.mockResolvedValue({
      components: [{ component: { id: 'product-b', kind: 'product', archived: false } }],
    });

    const res = await POST(ctx({ componentId: 'product-b', severity: 'major' }));

    expect(res.status).toBe(400);
    expect((await res.json()).error.message).toContain('not a leaf');
    expect(mockExists).not.toHaveBeenCalled();
    expect(mockLeaf).not.toHaveBeenCalled();
    expect(recordManualOverride).not.toHaveBeenCalled();
    expect(owner.publish).not.toHaveBeenCalled();
  });

  it('dispatches a valid owner leaf without any legacy DB or quorum validation', async () => {
    owner.configured.mockReturnValue(true);
    owner.projection.mockResolvedValue({
      components: [{ component: { id: 'sessions-api', kind: 'service', archived: false } }],
    });
    owner.incident.mockResolvedValue({
      incident: { id: 'incident-owner', title: 'down', summary: 'down' },
      timeline: [],
    });

    const res = await POST(ctx({ componentId: 'sessions-api', severity: 'major', title: 'down' }));

    expect(res.status).toBe(201);
    expect(owner.publish).toHaveBeenCalledOnce();
    expect(mockExists).not.toHaveBeenCalled();
    expect(mockLeaf).not.toHaveBeenCalled();
    expect(recordManualOverride).not.toHaveBeenCalled();
  });
});
