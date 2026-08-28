/**
 * Admin source registry endpoints (admin-session auth). Uses lib/sources.ts so
 * the engine's token scheme is the single source of truth.
 *   GET  /api/v1/admin/sources   list registered sources
 *   POST /api/v1/admin/sources   register a source → returns the token ONCE
 */
import type { APIRoute } from 'astro';
import { requireAdmin, ok, err } from '@/lib/admin-api';
import { createSource, listSources, type SourceKind } from '@/lib/sources';
import { nanoid } from 'nanoid';
import { generateToken } from '@/lib/api-tokens';
import {
  createSourceWithOwnerSaga,
  monitorOwnerProjection,
  ownerError,
  ownerRequestID,
  sourceAdminRow,
  sourceOwnerConfigured,
} from '../components/pulp-owner';

export const GET: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  if (sourceOwnerConfigured()) {
    try {
      const projection = await monitorOwnerProjection(true);
      // Token metadata is deliberately absent from the monitor projection.
      return ok(projection.sources.map(sourceAdminRow));
    } catch (error) {
      const failure = ownerError(error);
      return err('owner_unavailable', failure.message, failure.status);
    }
  }
  const rows = await listSources();
  // Never leak token_hash to the client.
  return ok(rows.map(({ tokenHash, ...rest }) => rest));
};

export const POST: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  const b = await ctx.request.json().catch(() => null);
  if (!b?.name) return err('bad_request', 'A source name is required.', 400);
  if (sourceOwnerConfigured()) {
    const id = nanoid();
    const token = generateToken();
    const now = new Date().toISOString();
    try {
      await createSourceWithOwnerSaga({
        request_id: ownerRequestID('source-lifecycle-create', ctx.request, id),
        source: {
          id,
          name: b.name,
          kind: (b.kind as string) ?? 'push',
          weight: b.weight != null ? Number(b.weight) : 1,
          trusted: b.trusted === true || b.trusted === 'true',
          direct_targets: false,
          default_ttl_seconds: b.defaultTtl != null && b.defaultTtl !== '' ? Number(b.defaultTtl) : null,
          revoked: false,
        },
        credential_id: nanoid(),
        token,
        actor_id: who,
        created_at: now,
      });
      // Exactly like legacy createSource: plaintext exists only in this response.
      return ok({ id, token, name: b.name }, 201);
    } catch (error) {
      const failure = ownerError(error);
      return err('owner_rejected', failure.message, failure.status);
    }
  }
  const result = await createSource({
    name: b.name,
    kind: (b.kind as SourceKind) ?? 'push',
    weight: b.weight != null ? Number(b.weight) : 1,
    trusted: b.trusted === true || b.trusted === 'true',
    defaultTtl: b.defaultTtl != null && b.defaultTtl !== '' ? Number(b.defaultTtl) : null,
  });
  // token returned exactly once.
  return ok(result, 201);
};
