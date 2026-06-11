/**
 * Admin single-source endpoints (admin-session auth).
 *   PATCH  /api/v1/admin/sources/:id   { action:'rotate' } rotate token (shown once)
 *                                       or { weight, defaultTtl, name }
 *   DELETE /api/v1/admin/sources/:id   revoke (archive) the source
 *
 * Token rotation writes a fresh hash via the same hashing scheme used in
 * lib/sources.ts (hashToken/generateToken), keeping one hashing scheme.
 */
import type { APIRoute } from 'astro';
import { db } from '@/db';
import { sources } from '@/db/schema';
import { eq } from 'drizzle-orm';
import { requireAdmin, ok, err } from '@/lib/admin-api';
import { revokeSource } from '@/lib/sources';
import { hashToken, generateToken } from '@/lib/api-tokens';

export const PATCH: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  const id = ctx.params.id!;
  const b = await ctx.request.json().catch(() => null);
  if (!b) return err('bad_request', 'Invalid JSON body.', 400);

  if (b.action === 'rotate') {
    const raw = generateToken();
    await db.update(sources).set({ tokenHash: hashToken(raw) }).where(eq(sources.id, id));
    return ok({ id, token: raw }); // shown once
  }

  const u: Record<string, unknown> = {};
  if (b.name) u.name = b.name;
  if (b.weight != null && b.weight !== '') u.weight = Number(b.weight);
  if (b.trusted !== undefined) u.trusted = b.trusted === true || b.trusted === 'true';
  if (b.defaultTtl !== undefined) u.defaultTtl = b.defaultTtl === '' || b.defaultTtl == null ? null : Number(b.defaultTtl);
  if (Object.keys(u).length) await db.update(sources).set(u).where(eq(sources.id, id));
  return ok({ id });
};

export const DELETE: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  await revokeSource(ctx.params.id!);
  return ok({ revoked: true });
};
