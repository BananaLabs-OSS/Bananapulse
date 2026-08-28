import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/lib/api-tokens', () => ({
  validateApiToken: vi.fn(async () => ({ id: 'token-1', scope: 'full' })),
}));
vi.mock('@/lib/pulp-bridge', () => ({
  pulpOwnerRouteFamilyConfigured: vi.fn(() => true),
}));
vi.mock('@/lib/admin-api', () => ({
  requireAdmin: vi.fn(async () => 'admin@example.test'),
  ok: vi.fn((data: unknown, status = 200) => new Response(JSON.stringify({ data }), { status })),
  err: vi.fn((code: string, message: string, status: number) => new Response(JSON.stringify({ error: { code, message } }), { status })),
}));
vi.mock('@/lib/components', () => ({
  componentExists: vi.fn(),
  isLeafComponent: vi.fn(),
}));
vi.mock('./owner', () => ({
  ownerCommandID: vi.fn((prefix: string, value: string) => `${prefix}:${value}`),
  ownerMaintenanceRows: vi.fn(async () => [{
    id: 'maintenance-1', title: 'Upgrade', summary: 'Routine', kind: 'scheduled',
    scheduledStart: '2026-07-26T01:00:00.000Z', scheduledEnd: '2026-07-26T02:00:00.000Z',
    affects: ['api'], createdAt: '2026-07-25T00:00:00.000Z',
  }]),
  ownerMaintenanceByID: vi.fn(async () => ({
    id: 'maintenance-1', title: 'Upgrade', summary: 'Routine', kind: 'scheduled',
    scheduled_start_unix: 1_785_000_000, scheduled_end_unix: 1_785_003_600,
    affects: ['api'], created_at_unix: 1_784_900_000,
  })),
  ownerNowUnix: vi.fn(() => 1_785_000_000),
  sendOwnerMaintenanceCommand: vi.fn(async () => undefined),
  toLegacyMaintenance: vi.fn((value: any) => ({
    id: value.id, title: value.title, summary: value.summary, kind: value.kind,
    scheduledStart: '2026-07-26T01:00:00.000Z', scheduledEnd: '2026-07-26T02:00:00.000Z',
    affects: value.affects, createdAt: '2026-07-25T00:00:00.000Z',
  })),
  toUnixSeconds: vi.fn((value: Date) => Math.floor(value.getTime() / 1_000)),
  validateOwnerAffects: vi.fn(async () => null),
}));
vi.mock('@/db', () => ({ db: {} }));
vi.mock('@/db/schema', () => ({ maintenance: {} }));

import { GET as tokenGet, POST as tokenPost } from './index';
import { PATCH as tokenPatch, DELETE as tokenDelete } from './[id]';
import { GET as adminGet, POST as adminPost } from '../admin/maintenance/index';
import { PATCH as adminPatch, DELETE as adminDelete } from '../admin/maintenance/[id]';
import {
  ownerMaintenanceRows,
  sendOwnerMaintenanceCommand,
  validateOwnerAffects,
} from './owner';
import { componentExists, isLeafComponent } from '@/lib/components';

beforeEach(() => vi.clearAllMocks());

const context = (url: string, init?: RequestInit, params: Record<string, string> = {}) => ({
  request: new Request(url, init),
  url: new URL(url),
  params,
  locals: {},
});

describe('maintenance owner compatibility', () => {
  it('keeps token GET camel-case row data while reading the state owner', async () => {
    const response = await tokenGet(context('https://status.example.test/api/v1/maintenance', {
      headers: { Authorization: 'Bearer token' },
    }) as any);
    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({ data: await ownerMaintenanceRows() });
  });

  it('keeps the snake-case token write input while sending a monitor maintenance command', async () => {
    const response = await tokenPost(context('https://status.example.test/api/v1/maintenance', {
      method: 'POST', headers: { Authorization: 'Bearer token', 'Content-Type': 'application/json' },
      body: JSON.stringify({
        title: 'Upgrade', summary: 'Routine', scheduled_start: '2026-07-26T01:00:00.000Z',
        scheduled_end: '2026-07-26T02:00:00.000Z', affects: ['api'],
      }),
    }) as any);
    expect(response.status).toBe(201);
    expect(validateOwnerAffects).toHaveBeenCalledWith(['api'], expect.any(Function));
    expect(sendOwnerMaintenanceCommand).toHaveBeenCalledWith(expect.objectContaining({
      version: 'monitor.v1', kind: 'schedule_maintenance',
      maintenance: expect.objectContaining({ title: 'Upgrade', affects: ['api'] }),
    }));
  });

  it('preserves token PATCH/DELETE responses and does not fall back after owner dispatch', async () => {
    const patch = await tokenPatch(context('https://status.example.test/api/v1/maintenance/maintenance-1', {
      method: 'PATCH', headers: { Authorization: 'Bearer token', 'Content-Type': 'application/json' },
      body: JSON.stringify({ summary: 'Extended' }),
    }, { id: 'maintenance-1' }) as any);
    expect(patch.status).toBe(200);
    expect(sendOwnerMaintenanceCommand).toHaveBeenCalledWith(expect.objectContaining({ kind: 'edit_maintenance' }));

    const remove = await tokenDelete(context('https://status.example.test/api/v1/maintenance/maintenance-1', {
      method: 'DELETE', headers: { Authorization: 'Bearer token' },
    }, { id: 'maintenance-1' }) as any);
    expect(await remove.json()).toEqual({ data: { deleted: true } });
    expect(sendOwnerMaintenanceCommand).toHaveBeenCalledWith(expect.objectContaining({ kind: 'delete_maintenance' }));
  });

  it('preserves admin GET/POST/PATCH/DELETE envelopes through the owner', async () => {
    const list = await adminGet(context('https://status.example.test/api/v1/admin/maintenance') as any);
    expect(await list.json()).toEqual({ data: await ownerMaintenanceRows() });

    const create = await adminPost(context('https://status.example.test/api/v1/admin/maintenance', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        title: 'Upgrade', summary: 'Routine', scheduledStart: '2026-07-26T01:00:00.000Z',
        scheduledEnd: '2026-07-26T02:00:00.000Z', affects: ['api'],
      }),
    }) as any);
    expect(create.status).toBe(201);

    const edit = await adminPatch(context('https://status.example.test/api/v1/admin/maintenance/maintenance-1', {
      method: 'PATCH', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: 'Changed', affects: ['api'] }),
    }, { id: 'maintenance-1' }) as any);
    expect(await edit.json()).toEqual({ data: { id: 'maintenance-1' } });

    const remove = await adminDelete(context('https://status.example.test/api/v1/admin/maintenance/maintenance-1', {
      method: 'DELETE',
    }, { id: 'maintenance-1' }) as any);
    expect(await remove.json()).toEqual({ data: { deleted: true } });
    expect(componentExists).not.toHaveBeenCalled();
    expect(isLeafComponent).not.toHaveBeenCalled();
  });
});
