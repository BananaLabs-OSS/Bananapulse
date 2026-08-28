/**
 * Admin single-source endpoints (admin-session auth).
 *   PATCH  /api/v1/admin/sources/:id   { action:'rotate' } rotate token (shown once)
 *                                       or { weight, defaultTtl, name }
 *   DELETE /api/v1/admin/sources/:id   revoke (archive) the source
 *
 * With the owner gate enabled token material goes directly to the private auth
 * owner and never enters the monitor or Lua composition.
 */
import type { APIRoute } from 'astro';
import { requireAdmin, ok, err } from '@/lib/admin-api';
import { revokeSource } from '@/lib/sources';
import { hashToken, generateToken } from '@/lib/api-tokens';
import { db } from '@/db';
import { sources } from '@/db/schema';
import { eq } from 'drizzle-orm';
import { nanoid } from 'nanoid';
import {
  monitorAdminCommand,
  monitorOwnerProjection,
  ownerError,
  ownerRequestID,
  revokeSourceWithOwnerSaga,
  rotateSourceCredential,
  sourceOwnerConfigured,
} from '../components/pulp-owner';

export const PATCH: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  const id = ctx.params.id!;
  const b = await ctx.request.json().catch(() => null);
  if (!b) return err('bad_request', 'Invalid JSON body.', 400);

  if (sourceOwnerConfigured()) {
    try {
      const projection = await monitorOwnerProjection(true);
      const source = projection.sources.find((value) => value.id === id);
      // Existing SQL PATCH is a successful no-op for absent source rows.
      if (!source) {
        if (b.action === 'rotate') return ok({ id, token: generateToken() });
        return ok({ id });
      }
      if (b.action === 'rotate') {
        const token = generateToken();
        await rotateSourceCredential({
          request_id: ownerRequestID('source-credential-rotate', ctx.request, id),
          credential_id: nanoid(),
          source_id: id,
          token,
          actor_id: who,
          rotated_at: new Date().toISOString(),
        });
        return ok({ id, token }); // shown once
      }
      const patch: Record<string, unknown> = { id };
      if (b.name) patch.name = b.name;
      if (b.weight != null && b.weight !== '') patch.weight = Number(b.weight);
      if (b.trusted !== undefined) patch.trusted = b.trusted === true || b.trusted === 'true';
      if (b.defaultTtl !== undefined) {
        patch.default_ttl_seconds = b.defaultTtl === '' || b.defaultTtl == null ? null : Number(b.defaultTtl);
        patch.default_ttl_seconds_set = true;
      }
      if (Object.keys(patch).length > 1) {
        await monitorAdminCommand({
          id: ownerRequestID('source-edit', ctx.request, id),
          kind: 'edit_source',
          source_patch: patch,
        });
      }
      return ok({ id });
    } catch (error) {
      const failure = ownerError(error);
      return err('owner_rejected', failure.message, failure.status);
    }
  }

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
  if (sourceOwnerConfigured()) {
    const id = ctx.params.id!;
    try {
      const projection = await monitorOwnerProjection(true);
      // Existing revoke is also a no-op when the row has vanished.
      if (projection.sources.some((source) => source.id === id)) {
        await revokeSourceWithOwnerSaga({
          request_id: ownerRequestID('source-lifecycle-revoke', ctx.request, id),
          source_id: id,
          actor_id: who,
          revoked_at: new Date().toISOString(),
        });
      }
      return ok({ revoked: true });
    } catch (error) {
      const failure = ownerError(error);
      return err('owner_rejected', failure.message, failure.status);
    }
  }
  await revokeSource(ctx.params.id!);
  return ok({ revoked: true });
};
