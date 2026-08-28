import { db } from '@/db';
import { apiTokens } from '@/db/schema';
import { eq, and, isNull } from 'drizzle-orm';
import { nanoid } from 'nanoid';
import { createHash, randomBytes } from 'node:crypto';

export function hashToken(token: string): string {
  return createHash('sha256').update(token).digest('hex');
}

export function generateToken(): string {
  return randomBytes(32).toString('hex');
}

export async function createApiToken(name: string, scope: string = 'full') {
  const raw = generateToken();
  const id = nanoid();
  await db.insert(apiTokens).values({
    id,
    name,
    tokenHash: hashToken(raw),
    scope,
  });
  return { id, token: raw, name, scope };
}

export async function validateApiToken(token: string) {
  const hash = hashToken(token);
  const rows = await db.select().from(apiTokens)
    .where(and(eq(apiTokens.tokenHash, hash), isNull(apiTokens.revokedAt)));
  const row = rows[0];
  if (!row) return null;
  if (row.expiresAt && row.expiresAt < new Date()) return null;
  await db.update(apiTokens).set({ lastUsedAt: new Date() }).where(eq(apiTokens.id, row.id));
  return row;
}

export async function revokeApiToken(id: string) {
  await db.update(apiTokens).set({ revokedAt: new Date() }).where(eq(apiTokens.id, id));
}

export async function listApiTokens() {
  return db.select().from(apiTokens).orderBy(apiTokens.createdAt);
}

/** Validate a Bearer token from the Authorization header and enforce a minimum scope level. Returns the token row, or null on any failure. */
export async function requireApiToken(request: Request, minScope: 'read' | 'write' | 'full') {
  const auth = request.headers.get('Authorization');
  if (!auth?.startsWith('Bearer ')) return null;
  const token = await validateApiToken(auth.slice(7));
  if (!token) return null;
  const scopeRank: Record<string, number> = { read: 1, write: 2, full: 3 };
  if ((scopeRank[token.scope] ?? 0) < (scopeRank[minScope] ?? 3)) return null;
  return token;
}
