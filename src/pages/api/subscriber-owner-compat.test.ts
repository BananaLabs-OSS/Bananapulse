import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/lib/subscribers', () => ({
  addSubscriber: vi.fn(),
  confirmSubscriber: vi.fn(),
  removeSubscriber: vi.fn(),
}));
vi.mock('@/lib/pulp-bridge', () => ({
  pulpSubscriberLifecycleConfigured: vi.fn(() => true),
  subscriberOwnerIdentity: vi.fn(() => ({
    requestId: 'subscribe-request',
    confirmationToken: 'confirm-token',
    unsubscribeToken: 'unsubscribe-token',
  })),
  subscriberTokenRequestID: vi.fn((purpose: string) => `${purpose}-request`),
  subscribeWithOwner: vi.fn(async () => ({ version: 'bananapulse.subscribers/v1', created: true })),
  confirmWithOwner: vi.fn(async () => ({ version: 'bananapulse.subscribers/v1', confirmed: true })),
  unsubscribeWithOwner: vi.fn(async () => ({ version: 'bananapulse.subscribers/v1', unsubscribed: true })),
}));

import { addSubscriber, confirmSubscriber, removeSubscriber } from '@/lib/subscribers';
import {
  confirmWithOwner,
  subscribeWithOwner,
  unsubscribeWithOwner,
} from '@/lib/pulp-bridge';
import { POST as subscribe } from './subscribe';
import { GET as confirm } from './subscribe/confirm';
import { GET as unsubscribe } from './unsubscribe';

beforeEach(() => vi.clearAllMocks());

describe('public subscription owner compatibility', () => {
  it('keeps POST response and validation while routing valid writes through Pulp', async () => {
    const request = new Request('https://status.example.test/api/subscribe', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Forwarded-For': '203.0.113.10' },
      body: JSON.stringify({ email: 'Person@Example.test' }),
    });
    const response = await subscribe({ request, url: new URL(request.url) } as any);
    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({ ok: true });
    expect(addSubscriber).not.toHaveBeenCalled();
    expect(subscribeWithOwner).toHaveBeenCalledWith(expect.objectContaining({
      request_id: 'subscribe-request',
      email: 'person@example.test',
      confirmation_token: 'confirm-token',
      unsubscribe_token: 'unsubscribe-token',
      confirmation_body: expect.stringContaining(
        'https://status.example.test/api/subscribe/confirm?token=confirm-token',
      ),
    }));
  });

  it('preserves the confirmation redirect and invalid-token 404', async () => {
    const success = await confirm({
      url: new URL('https://status.example.test/api/subscribe/confirm?token=confirm-token'),
    } as any);
    expect(success.status).toBe(302);
    expect(success.headers.get('Location')).toBe('/?subscribed=1');
    expect(confirmSubscriber).not.toHaveBeenCalled();

    vi.mocked(confirmWithOwner).mockRejectedValueOnce(new Error('invalid token'));
    const missing = await confirm({
      url: new URL('https://status.example.test/api/subscribe/confirm?token=bad'),
    } as any);
    expect(missing.status).toBe(404);
    expect(await missing.text()).toBe('Confirmation link not found or already used.');
  });

  it('keeps unsubscribe idempotent and never falls back after an owner attempt', async () => {
    vi.mocked(unsubscribeWithOwner).mockRejectedValueOnce(new Error('already gone'));
    const response = await unsubscribe({
      url: new URL('https://status.example.test/api/unsubscribe?token=unsubscribe-token'),
    } as any);
    expect(response.status).toBe(302);
    expect(response.headers.get('Location')).toBe('/?unsubscribed=1');
    expect(removeSubscriber).not.toHaveBeenCalled();
  });
});

