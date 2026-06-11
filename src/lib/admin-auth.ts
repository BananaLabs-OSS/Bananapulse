import { db } from '@/db';
import { adminMagicLinks, adminSessions } from '@/db/schema';
import { eq, and, isNull } from 'drizzle-orm';
import { randomBytes, createHmac, timingSafeEqual } from 'node:crypto';

const MAGIC_LINK_TTL_MS = 15 * 60 * 1000;
const SESSION_TTL_MS = 7 * 24 * 60 * 60 * 1000;
const COOKIE_NAME = 'status_admin_session';

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
