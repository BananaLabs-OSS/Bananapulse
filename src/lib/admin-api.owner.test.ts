import { beforeEach, describe, expect, it, vi } from 'vitest';

const auth = vi.hoisted(() => ({
  verifyCookie: vi.fn(() => 'owner-session-token'),
  validateSession: vi.fn(),
  validateSessionWithOwner: vi.fn(),
}));
const bridge = vi.hoisted(() => ({
  ownerConfigured: vi.fn(),
}));

vi.mock('./admin-auth', () => ({
  COOKIE_NAME: 'status_admin_session',
  verifyCookie: auth.verifyCookie,
  validateSession: auth.validateSession,
  validateSessionWithOwner: auth.validateSessionWithOwner,
}));
vi.mock('./pulp-bridge', () => ({
  pulpOwnerRouteFamilyConfigured: bridge.ownerConfigured,
}));

import { adminEmailFromRequest, requireAdmin } from './admin-api';

function context() {
  return {
    locals: {},
    cookies: {
      get: vi.fn(() => ({ value: 'signed-session-cookie' })),
    },
  } as any;
}

beforeEach(() => {
  vi.clearAllMocks();
  process.env.ADMIN_SESSION_SECRET = 'test-session-secret';
});

describe('admin API auth-owner selection', () => {
  it('validates the signed cookie only with the auth owner when selected', async () => {
    bridge.ownerConfigured.mockReturnValue(true);
    auth.validateSessionWithOwner.mockResolvedValue('admin@example.test');

    await expect(adminEmailFromRequest(context())).resolves.toBe('admin@example.test');
    expect(bridge.ownerConfigured).toHaveBeenCalledWith('auth');
    expect(auth.validateSessionWithOwner).toHaveBeenCalledWith('owner-session-token');
    expect(auth.validateSession).not.toHaveBeenCalled();
  });

  it('fails closed when owner validation is unavailable', async () => {
    bridge.ownerConfigured.mockReturnValue(true);
    auth.validateSessionWithOwner.mockRejectedValue(new Error('owner unavailable'));

    await expect(requireAdmin(context())).rejects.toThrow('owner unavailable');
    expect(auth.validateSession).not.toHaveBeenCalled();
  });

  it('retains legacy session validation only when auth-owner mode is off', async () => {
    bridge.ownerConfigured.mockReturnValue(false);
    auth.validateSession.mockResolvedValue('legacy@example.test');

    await expect(adminEmailFromRequest(context())).resolves.toBe('legacy@example.test');
    expect(auth.validateSession).toHaveBeenCalledWith('owner-session-token');
    expect(auth.validateSessionWithOwner).not.toHaveBeenCalled();
  });

  it('preserves the 401 envelope for a normal owner-declared invalid session', async () => {
    bridge.ownerConfigured.mockReturnValue(true);
    auth.validateSessionWithOwner.mockResolvedValue(null);

    const response = await requireAdmin(context());
    expect(response).toBeInstanceOf(Response);
    expect((response as Response).status).toBe(401);
    expect(await (response as Response).json()).toEqual({
      error: { code: 'unauthorized', message: 'Admin session required.' },
    });
    expect(auth.validateSession).not.toHaveBeenCalled();
  });
});
