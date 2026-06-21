/**
 * Admin maintenance item (admin-session auth).
 *   PATCH  /api/v1/admin/maintenance/:id
 *   DELETE /api/v1/admin/maintenance/:id
 */
import type { APIRoute } from 'astro';
import { db } from '@/db';
import { maintenance } from '@/db/schema';
import { eq } from 'drizzle-orm';
import { requireAdmin, ok, err } from '@/lib/admin-api';
import { componentExists, isLeafComponent } from '@/lib/components';

export const PATCH: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  const id = ctx.params.id!;
  const b = await ctx.request.json().catch(() => null);
  if (!b) return err('bad_request', 'Invalid JSON body.', 400);
  const u: Record<string, unknown> = {};
  if (b.title) u.title = b.title;
  if (b.summary) u.summary = b.summary;
  if (b.scheduledStart) u.scheduledStart = new Date(b.scheduledStart);
  if (b.scheduledEnd) u.scheduledEnd = new Date(b.scheduledEnd);
  if (Array.isArray(b.affects)) {
    // Validate every submitted component ID exists and is a leaf, same rule as
    // POST — an unvalidated edit could silently make the window invisible.
    for (const a of b.affects) {
      if (!(await componentExists(a))) return err('bad_request', `Unknown component "${a}".`, 400);
      if (!(await isLeafComponent(a))) return err('bad_request', `Component "${a}" is not a leaf (schedule on a service or host).`, 400);
    }
    u.affects = b.affects;
  }
  if (Object.keys(u).length) await db.update(maintenance).set(u).where(eq(maintenance.id, id));
  return ok({ id });
};

export const DELETE: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  await db.delete(maintenance).where(eq(maintenance.id, ctx.params.id!));
  return ok({ deleted: true });
};
