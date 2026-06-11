/**
 * POST /api/v1/admin/incidents/:id/updates — post a human update.
 *
 * Pure narration: appends a timeline entry attributed to the admin. Does not
 * touch the incident's existence (engine-owned). May carry a status label.
 */
import type { APIRoute } from 'astro';
import { db } from '@/db';
import { incidents, incidentTimeline } from '@/db/schema';
import { eq } from 'drizzle-orm';
import { nanoid } from 'nanoid';
import { requireAdmin, ok, err } from '@/lib/admin-api';
import { notifyIncident } from '@/lib/notify';

export const POST: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  const id = ctx.params.id!;
  const rows = await db.select().from(incidents).where(eq(incidents.id, id));
  if (!rows[0]) return err('not_found', 'Incident not found.', 404);

  const body = await ctx.request.json().catch(() => null);
  if (!body?.body) return err('bad_request', 'An update body is required.', 400);

  const label = (body.label || rows[0].status || 'update').toString().toUpperCase();
  const updateId = nanoid();
  await db.insert(incidentTimeline).values({
    id: updateId, incidentId: id, at: new Date(),
    label, body: body.body, author: who,
  });
  // Fan out the human-posted narration. A 'resolved' label is a resolution.
  await notifyIncident(id, label === 'RESOLVED' ? 'resolved' : 'update');
  return ok({ id: updateId, incidentId: id, label, body: body.body }, 201);
};
