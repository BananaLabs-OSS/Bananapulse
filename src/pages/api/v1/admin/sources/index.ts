/**
 * Admin source registry endpoints (admin-session auth). Uses lib/sources.ts so
 * the engine's token scheme is the single source of truth.
 *   GET  /api/v1/admin/sources   list registered sources
 *   POST /api/v1/admin/sources   register a source → returns the token ONCE
 */
import type { APIRoute } from 'astro';
import { requireAdmin, ok, err } from '@/lib/admin-api';
import { createSource, listSources, type SourceKind } from '@/lib/sources';

export const GET: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  const rows = await listSources();
  // Never leak token_hash to the client.
  return ok(rows.map(({ tokenHash, ...rest }) => rest));
};

export const POST: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  const b = await ctx.request.json().catch(() => null);
  if (!b?.name) return err('bad_request', 'A source name is required.', 400);
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
