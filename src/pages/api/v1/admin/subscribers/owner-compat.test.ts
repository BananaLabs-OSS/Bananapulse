import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/lib/admin-api', () => ({
  requireAdmin: vi.fn(async () => 'admin@example.test'),
  ok: vi.fn((data: unknown, status = 200) => new Response(JSON.stringify({ data }), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })),
}));
vi.mock('@/lib/subscribers', () => ({ listSubscribers: vi.fn() }));
vi.mock('@/lib/pulp-bridge', () => ({
  pulpOwnerRouteFamilyConfigured: vi.fn(() => true),
  listSubscribersWithOwner: vi.fn(async () => ({
    version: 'bananapulse.subscribers/v1',
    subscribers: [{
      id: 'subscriber-1',
      email: 'person@example.test',
      state: 'confirmed',
      confirmedAt: '2026-07-26T00:00:00.000Z',
      createdAt: '2026-07-25T00:00:00.000Z',
    }],
  })),
  deleteSubscriberWithOwner: vi.fn(async () => ({
    version: 'bananapulse.subscribers/v1',
    found: true,
    changed: true,
  })),
  pulpOwnerRequestID: vi.fn(() => 'delete-request-1'),
}));

import { listSubscribers } from '@/lib/subscribers';
import {
  deleteSubscriberWithOwner,
  listSubscribersWithOwner,
} from '@/lib/pulp-bridge';
import { GET } from './index';
import { DELETE } from './[id]';

beforeEach(() => vi.clearAllMocks());

describe('admin subscriber owner compatibility', () => {
  it('preserves the legacy PII row shape without exposing owner state metadata', async () => {
    const response = await GET({
      request: new Request('https://status.example.test/api/v1/admin/subscribers'),
      url: new URL('https://status.example.test/api/v1/admin/subscribers'),
      locals: {},
    } as any);
    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({
      data: [{
        id: 'subscriber-1',
        email: 'person@example.test',
        confirmedAt: '2026-07-26T00:00:00.000Z',
        createdAt: '2026-07-25T00:00:00.000Z',
      }],
    });
    expect(listSubscribersWithOwner).toHaveBeenCalledOnce();
    expect(listSubscribers).not.toHaveBeenCalled();
  });

  it('preserves idempotent delete response through the owner', async () => {
    const response = await DELETE({
      request: new Request('https://status.example.test/api/v1/admin/subscribers/subscriber-1', { method: 'DELETE' }),
      url: new URL('https://status.example.test/api/v1/admin/subscribers/subscriber-1'),
      params: { id: 'subscriber-1' },
      locals: {},
    } as any);
    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({ data: { deleted: true } });
    expect(deleteSubscriberWithOwner).toHaveBeenCalledWith('delete-request-1', 'subscriber-1');
  });
});

