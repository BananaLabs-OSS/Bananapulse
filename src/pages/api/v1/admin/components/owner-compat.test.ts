import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/lib/admin-api', () => ({
  requireAdmin: vi.fn(async () => 'admin@example.test'),
  ok: vi.fn((data: unknown, status = 200) => new Response(JSON.stringify({ data }), { status })),
  err: vi.fn((code: string, message: string, status: number) => new Response(JSON.stringify({ error: { code, message } }), { status })),
}));
vi.mock('@/lib/db-components', () => ({
  getComponentsAdmin: vi.fn(),
  getComponentRow: vi.fn(),
  createComponentRow: vi.fn(),
  updateComponentRow: vi.fn(),
  setComponentArchived: vi.fn(),
  ArchiveBlockedError: class ArchiveBlockedError extends Error {},
}));
vi.mock('@/lib/components', () => ({ componentExists: vi.fn(), descendantIds: vi.fn() }));
vi.mock('./pulp-owner', () => ({
  monitorAdminOwnerConfigured: vi.fn(() => true),
  monitorOwnerProjection: vi.fn(async () => ({
    components: [{ component: {
      id: 'api', name: 'API', kind: 'service', fallback_status: 'operational',
      sort_order: 2, archived: false, created_at_unix: 1,
    } }], sources: [], mappings: [],
  })),
  componentAdminRow: vi.fn((value) => ({ id: value.id, status: 'ok' })),
  monitorAdminCommand: vi.fn(async () => ({ command_id: 'command-1' })),
  ownerRequestID: vi.fn(() => 'command-1'),
  ownerError: vi.fn(() => ({ status: 502, message: 'owner failed' })),
}));

import {
  createComponentRow,
  getComponentRow,
  getComponentsAdmin,
  updateComponentRow,
} from '@/lib/db-components';
import { componentExists, descendantIds } from '@/lib/components';
import {
  componentAdminRow,
  monitorAdminCommand,
  monitorOwnerProjection,
} from './pulp-owner';
import { GET, POST } from './index';
import { PATCH } from './[id]';

beforeEach(() => vi.clearAllMocks());

describe('component owner HTTP compatibility', () => {
  it('projects monitor state into the legacy admin list without reading SQL', async () => {
    const response = await GET({
      request: new Request('https://pulse.test/api/v1/admin/components'),
      url: new URL('https://pulse.test/api/v1/admin/components'), locals: {},
    } as any);
    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({ data: [{ id: 'api', status: 'ok' }] });
    expect(componentAdminRow).toHaveBeenCalledOnce();
    expect(getComponentsAdmin).not.toHaveBeenCalled();
  });

  it('creates through the state owner and preserves the 201 response', async () => {
    const response = await POST({
      request: new Request('https://pulse.test/api/v1/admin/components', {
        method: 'POST', headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ id: 'web', name: 'Web', kind: 'service', sortOrder: 4 }),
      }),
      url: new URL('https://pulse.test/api/v1/admin/components'), locals: {},
    } as any);
    expect(response.status).toBe(201);
    expect(await response.json()).toEqual({ data: { id: 'web' } });
    expect(monitorAdminCommand).toHaveBeenCalledWith(expect.objectContaining({
      kind: 'upsert_component',
      component: expect.objectContaining({ id: 'web', sort_order: 4, launched: true }),
    }));
    expect(createComponentRow).not.toHaveBeenCalled();
    expect(getComponentRow).not.toHaveBeenCalled();
    expect(componentExists).not.toHaveBeenCalled();
  });

  it('validates an edited parent from owner projection without legacy tree queries', async () => {
    vi.mocked(monitorOwnerProjection).mockResolvedValueOnce({
      components: [
        { component: { id: 'api', name: 'API', kind: 'service', archived: false } },
        { component: { id: 'root', name: 'Root', kind: 'product', archived: false } },
      ],
      sources: [], mappings: [],
    } as any);

    const response = await PATCH({
      request: new Request('https://pulse.test/api/v1/admin/components/api', {
        method: 'PATCH', headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ parentId: 'root' }),
      }),
      url: new URL('https://pulse.test/api/v1/admin/components/api'),
      params: { id: 'api' }, locals: {},
    } as any);

    expect(response.status).toBe(200);
    expect(monitorAdminCommand).toHaveBeenCalledWith(expect.objectContaining({
      kind: 'edit_component',
      component_patch: expect.objectContaining({ id: 'api', parent_id: 'root' }),
    }));
    expect(componentExists).not.toHaveBeenCalled();
    expect(descendantIds).not.toHaveBeenCalled();
    expect(updateComponentRow).not.toHaveBeenCalled();
  });
});
