/**
 * Admin maintenance endpoints (admin-session auth).
 *   GET  /api/v1/admin/maintenance   list
 *   POST /api/v1/admin/maintenance   schedule a planned window
 */
import type { APIRoute } from 'astro';
import { db } from '@/db';
import { maintenance } from '@/db/schema';
import { asc } from 'drizzle-orm';
import { nanoid } from 'nanoid';
import { requireAdmin, ok, err } from '@/lib/admin-api';
import { componentExists, isLeafComponent } from '@/lib/components';

export const GET: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  const rows = await db.select().from(maintenance).orderBy(asc(maintenance.scheduledStart));
  return ok(rows);
};

export const POST: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  const b = await ctx.request.json().catch(() => null);
  if (!b?.title || !b?.summary || !b?.scheduledStart || !b?.scheduledEnd || !Array.isArray(b.affects) || !b.affects.length) {
    return err('bad_request', 'title, summary, scheduledStart, scheduledEnd and at least one affected component are required.', 400);
  }
  const startMs = new Date(b.scheduledStart).getTime();
  const endMs = new Date(b.scheduledEnd).getTime();
  if (!Number.isFinite(startMs) || !Number.isFinite(endMs)) {
    return err('bad_request', 'scheduledStart and scheduledEnd must be valid date-times.', 400);
  }
  if (endMs <= startMs) {
    return err('bad_request', 'scheduledEnd must be after scheduledStart.', 400);
  }
  // Every affected id must resolve to a real LEAF component, or the window
  // renders on no page (the invisible-maintenance bug).
  for (const a of b.affects) {
    if (!(await componentExists(a))) return err('bad_request', `Unknown component "${a}".`, 400);
    if (!(await isLeafComponent(a))) return err('bad_request', `Component "${a}" is not a leaf (schedule on a service or host).`, 400);
  }
  const id = nanoid();
  await db.insert(maintenance).values({
    id, title: b.title, summary: b.summary,
    scheduledStart: new Date(b.scheduledStart),
    scheduledEnd: new Date(b.scheduledEnd),
    affects: b.affects,
  });
  return ok({ id }, 201);
};
