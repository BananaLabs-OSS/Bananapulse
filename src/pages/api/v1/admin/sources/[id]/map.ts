/**
 * POST /api/v1/admin/sources/:id/map — add a raw_label → component mapping.
 * GET — list this source's mappings.
 * Uses lib/sources.ts mapTarget so vendor vocabulary only ever enters via the
 * source_target_map, as the engine expects.
 */
import type { APIRoute } from 'astro';
import { db } from '@/db';
import { sourceTargetMap } from '@/db/schema';
import { eq } from 'drizzle-orm';
import { requireAdmin, ok, err } from '@/lib/admin-api';
import { mapTarget, removeMapping } from '@/lib/sources';
import { componentExists } from '@/lib/components';

export const GET: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  const rows = await db.select().from(sourceTargetMap).where(eq(sourceTargetMap.sourceId, ctx.params.id!));
  return ok(rows);
};

export const POST: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  const b = await ctx.request.json().catch(() => null);
  if (!b?.rawLabel || !b?.componentId) return err('bad_request', 'rawLabel and componentId are required.', 400);
  if (!(await componentExists(b.componentId))) return err('bad_request', `Unknown component "${b.componentId}".`, 400);
  const id = await mapTarget(ctx.params.id!, b.rawLabel, b.componentId);
  return ok({ id }, 201);
};

export const DELETE: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  const mapId = new URL(ctx.request.url).searchParams.get('mapId') ?? '';
  if (!mapId) return err('bad_request', 'mapId is required.', 400);
  await removeMapping(mapId);
  return ok({ removed: mapId });
};
