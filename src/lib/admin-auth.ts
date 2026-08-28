import { db } from '@/db';
import { adminMagicLinks, adminSessions } from '@/db/schema';
import { eq, and, isNull } from 'drizzle-orm';
import { randomBytes, createHmac, timingSafeEqual } from 'node:crypto';
import {
  callPulpEvent,
  PulpBridgeError,
  pulpOwnerRequestID,
} from '@/lib/pulp-bridge';

const MAGIC_LINK_TTL_MS = 15 * 60 * 1000;
const SESSION_TTL_MS = 7 * 24 * 60 * 60 * 1000;
const COOKIE_NAME = 'status_admin_session';
const AUTH_VERSION = 'bananapulse.auth/v1';

// Auth commands deliberately bypass Lua: plaintext credentials only traverse
// the private web host -> authenticated Pulp bridge -> auth owner path.
const AUTH_EVENTS = {
  magicLinkIssue: 'bananapulse.host.auth.magic-link.issue.v1',
  magicLinkConsume: 'bananapulse.host.auth.magic-link.consume.v1',
  sessionCreate: 'bananapulse.host.auth.session.create.v1',
  sessionValidate: 'bananapulse.host.auth.session.validate.v1',
  sessionRevoke: 'bananapulse.host.auth.session.revoke.v1',
} as const;

export { COOKIE_NAME };

export function generateMagicToken(): string {
  return randomBytes(32).toString('hex');
}

export function generateSessionId(): string {
  return randomBytes(32).toString('hex');
}

export function signCookie(value: string, secret: string): string {
  const sig = createHmac('sha256', secret).update(value).digest('hex');
  return `${value}.${sig}`;
}

export function verifyCookie(signed: string, secret: string): string | null {
  const idx = signed.lastIndexOf('.');
  if (idx === -1) return null;
  const value = signed.slice(0, idx);
  const sig = signed.slice(idx + 1);
  const expected = createHmac('sha256', secret).update(value).digest('hex');
  if (sig.length !== expected.length) return null;
  if (!timingSafeEqual(Buffer.from(sig), Buffer.from(expected))) return null;
  return value;
}

function ownerActionRequestID(action: string, opaque: string): string {
  // The bridge receives a stable opaque identifier, never the plaintext token.
  // A fresh nonce avoids replaying a previous user action as an owner command.
  return pulpOwnerRequestID(`admin-auth-${action}`, `${opaque}\0${generateMagicToken()}`);
}

function invalidOwnerAuthResponse(operation: string): never {
  throw new PulpBridgeError(`Pulp auth ${operation} returned an invalid response.`, 502);
}

type OwnerMagicLinkIssueResult = {
  version: string;
  accepted: boolean;
  deliver: boolean;
  challenge_id?: string;
};

type OwnerMagicLinkConsumeResult = {
  version: string;
  authenticated: boolean;
  challenge_id?: string;
  identity_id?: string;
};

type OwnerSessionCreateResult = {
  version: string;
  session_id: string;
  created: boolean;
};

type OwnerSessionValidateResult = {
  version: string;
  valid: boolean;
  email?: string;
};

type OwnerSessionRevokeResult = {
  version: string;
  revoked: boolean;
};

/**
 * Issues a one-use, 15-minute challenge through the private auth owner.
 * `deliver: false` is the intentional anti-enumeration response for unknown
 * or disabled identities and is not an error.
 */
export async function issueMagicLinkWithOwner(email: string): Promise<{
  token: string;
  deliver: boolean;
}> {
  const token = generateMagicToken();
  const issuedAt = new Date();
  const expiresAt = new Date(issuedAt.getTime() + MAGIC_LINK_TTL_MS);
  const result = await callPulpEvent<Record<string, string>, OwnerMagicLinkIssueResult>(
    AUTH_EVENTS.magicLinkIssue,
    {
      version: AUTH_VERSION,
      request_id: ownerActionRequestID('magic-link-issue', token),
      email,
      token,
      issued_at: issuedAt.toISOString(),
      expires_at: expiresAt.toISOString(),
    },
  );
  if (
    result.version !== AUTH_VERSION ||
    typeof result.accepted !== 'boolean' ||
    typeof result.deliver !== 'boolean' ||
    !result.accepted
  ) {
    invalidOwnerAuthResponse('magic-link.issue');
  }
  return { token, deliver: result.deliver };
}

/** Returns the consumed challenge identity, or null for a normal invalid link. */
export async function consumeMagicLinkWithOwner(token: string): Promise<{
  challengeId: string;
  identityId: string;
} | null> {
  const result = await callPulpEvent<Record<string, string>, OwnerMagicLinkConsumeResult>(
    AUTH_EVENTS.magicLinkConsume,
    {
      version: AUTH_VERSION,
      request_id: ownerActionRequestID('magic-link-consume', token),
      token,
      consumed_at: new Date().toISOString(),
    },
  );
  if (result.version !== AUTH_VERSION || typeof result.authenticated !== 'boolean') {
    invalidOwnerAuthResponse('magic-link.consume');
  }
  if (!result.authenticated) return null;
  if (!result.challenge_id || !result.identity_id) invalidOwnerAuthResponse('magic-link.consume');
  return { challengeId: result.challenge_id, identityId: result.identity_id };
}

/** Creates a seven-day session; the caller owns its cookie signature only. */
export async function createSessionWithOwner(challengeId: string, identityId: string): Promise<string> {
  const token = generateSessionId();
  const issuedAt = new Date();
  const expiresAt = new Date(issuedAt.getTime() + SESSION_TTL_MS);
  const result = await callPulpEvent<Record<string, string>, OwnerSessionCreateResult>(
    AUTH_EVENTS.sessionCreate,
    {
      version: AUTH_VERSION,
      request_id: ownerActionRequestID('session-create', token),
      challenge_id: challengeId,
      identity_id: identityId,
      token,
      issued_at: issuedAt.toISOString(),
      expires_at: expiresAt.toISOString(),
    },
  );
  if (
    result.version !== AUTH_VERSION ||
    result.created !== true ||
    typeof result.session_id !== 'string' ||
    !result.session_id
  ) {
    invalidOwnerAuthResponse('session.create');
  }
  return token;
}

/** Returns the owner-authenticated email, or null for an invalid session. */
export async function validateSessionWithOwner(sessionToken: string): Promise<string | null> {
  const result = await callPulpEvent<Record<string, string>, OwnerSessionValidateResult>(
    AUTH_EVENTS.sessionValidate,
    {
      version: AUTH_VERSION,
      token: sessionToken,
      at: new Date().toISOString(),
    },
  );
  if (result.version !== AUTH_VERSION || typeof result.valid !== 'boolean') {
    invalidOwnerAuthResponse('session.validate');
  }
  if (!result.valid) return null;
  if (!result.email || typeof result.email !== 'string') invalidOwnerAuthResponse('session.validate');
  return result.email;
}

/** Revokes the owner session. An unavailable owner is an error; never fall back. */
export async function destroySessionWithOwner(sessionToken: string): Promise<void> {
  const result = await callPulpEvent<Record<string, string>, OwnerSessionRevokeResult>(
    AUTH_EVENTS.sessionRevoke,
    {
      version: AUTH_VERSION,
      request_id: ownerActionRequestID('session-revoke', sessionToken),
      token: sessionToken,
      revoked_at: new Date().toISOString(),
    },
  );
  if (result.version !== AUTH_VERSION || typeof result.revoked !== 'boolean') {
    invalidOwnerAuthResponse('session.revoke');
  }
}

export async function createMagicLink(email: string): Promise<string> {
  const token = generateMagicToken();
  await db.insert(adminMagicLinks).values({
    token,
    email,
    expiresAt: new Date(Date.now() + MAGIC_LINK_TTL_MS),
  });
  return token;
}

export async function consumeMagicLink(token: string): Promise<string | null> {
  // Atomic claim: one conditional UPDATE flips used_at only if still unused,
  // so a single link can never mint two sessions even under a concurrent race.
  const now = new Date();
  const claimed = await db.update(adminMagicLinks)
    .set({ usedAt: now })
    .where(and(eq(adminMagicLinks.token, token), isNull(adminMagicLinks.usedAt)))
    .returning({ email: adminMagicLinks.email, expiresAt: adminMagicLinks.expiresAt });
  const link = claimed[0];
  if (!link) return null;            // already consumed (or unknown token)
  if (link.expiresAt < now) return null; // expired — claimed but not honored
  return link.email;
}

export async function createSession(email: string): Promise<string> {
  const sessionId = generateSessionId();
  await db.insert(adminSessions).values({
    token: sessionId,
    email,
    expiresAt: new Date(Date.now() + SESSION_TTL_MS),
  });
  return sessionId;
}

export async function validateSession(sessionId: string): Promise<string | null> {
  const rows = await db.select().from(adminSessions).where(eq(adminSessions.token, sessionId));
  const session = rows[0];
  if (!session) return null;
  if (session.expiresAt < new Date()) return null;
  return session.email;
}

export async function destroySession(sessionId: string) {
  await db.delete(adminSessions).where(eq(adminSessions.token, sessionId));
}
