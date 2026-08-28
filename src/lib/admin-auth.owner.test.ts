import { beforeEach, describe, expect, it, vi } from 'vitest';

const bridge = vi.hoisted(() => ({
  callPulpEvent: vi.fn(),
  pulpOwnerRequestID: vi.fn((prefix: string) => `${prefix}-id`),
}));

vi.mock('@/lib/pulp-bridge', async () => {
  const actual = await vi.importActual<typeof import('@/lib/pulp-bridge')>('@/lib/pulp-bridge');
  return {
    ...actual,
    callPulpEvent: bridge.callPulpEvent,
    pulpOwnerRequestID: bridge.pulpOwnerRequestID,
  };
});

import {
  consumeMagicLinkWithOwner,
  createSessionWithOwner,
  destroySessionWithOwner,
  issueMagicLinkWithOwner,
  validateSessionWithOwner,
} from './admin-auth';

beforeEach(() => vi.clearAllMocks());

describe('pulp-auth private host contracts', () => {
  it('issues a 15-minute link and honors the anti-enumeration delivery decision', async () => {
    bridge.callPulpEvent.mockResolvedValue({
      version: 'bananapulse.auth/v1',
      accepted: true,
      deliver: false,
    });

    const issued = await issueMagicLinkWithOwner('admin@example.test');

    expect(issued.deliver).toBe(false);
    expect(issued.token).toMatch(/^[a-f0-9]{64}$/);
    expect(bridge.callPulpEvent).toHaveBeenCalledWith(
      'bananapulse.host.auth.magic-link.issue.v1',
      expect.objectContaining({
        version: 'bananapulse.auth/v1',
        email: 'admin@example.test',
        token: issued.token,
      }),
    );
    const request = bridge.callPulpEvent.mock.calls[0][1];
    expect(Date.parse(request.expires_at) - Date.parse(request.issued_at)).toBe(15 * 60 * 1000);
  });

  it('treats an invalid magic link as a normal unauthenticated result', async () => {
    bridge.callPulpEvent.mockResolvedValue({
      version: 'bananapulse.auth/v1',
      authenticated: false,
    });
    await expect(consumeMagicLinkWithOwner('opaque-link-token')).resolves.toBeNull();
    expect(bridge.callPulpEvent).toHaveBeenCalledWith(
      'bananapulse.host.auth.magic-link.consume.v1',
      expect.objectContaining({ token: 'opaque-link-token' }),
    );
  });

  it('creates a seven-day owner session while retaining the opaque cookie token locally', async () => {
    bridge.callPulpEvent.mockResolvedValue({
      version: 'bananapulse.auth/v1',
      session_id: 'session_owner_id',
      created: true,
    });

    const token = await createSessionWithOwner('challenge_1', 'identity_1');
    expect(token).toMatch(/^[a-f0-9]{64}$/);
    const request = bridge.callPulpEvent.mock.calls[0][1];
    expect(Date.parse(request.expires_at) - Date.parse(request.issued_at)).toBe(7 * 24 * 60 * 60 * 1000);
    expect(request).toMatchObject({ challenge_id: 'challenge_1', identity_id: 'identity_1', token });
  });

  it('uses owner session validation and revocation events only', async () => {
    bridge.callPulpEvent
      .mockResolvedValueOnce({ version: 'bananapulse.auth/v1', valid: true, email: 'admin@example.test' })
      .mockResolvedValueOnce({ version: 'bananapulse.auth/v1', revoked: true });

    await expect(validateSessionWithOwner('session-token')).resolves.toBe('admin@example.test');
    await expect(destroySessionWithOwner('session-token')).resolves.toBeUndefined();
    expect(bridge.callPulpEvent.mock.calls.map((call) => call[0] as string)).toEqual([
      'bananapulse.host.auth.session.validate.v1',
      'bananapulse.host.auth.session.revoke.v1',
    ]);
  });
});
