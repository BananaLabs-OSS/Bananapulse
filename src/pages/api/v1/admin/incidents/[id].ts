/**
 * Admin single-incident endpoints (admin-session auth).
 *
 *   GET   /api/v1/admin/incidents/:id     incident + timeline
 *   PATCH /api/v1/admin/incidents/:id     human-owned status move, or level override
 *
 * Status (investigating→identified→monitoring→resolved) is human-owned, so a
 * status move writes the field + a timeline entry directly (that is narration,
 * not status-flipping the engine's truth). A LEVEL override, by contrast, is an
 * engine concern — it routes through recordManualOverride so the read model and
 * the incident agree, and the incident becomes human-owned (auto=false).
 */
import type { APIRoute } from 'astro';
import { db } from '@/db';
import { incidents, incidentTimeline } from '@/db/schema';
import { eq, desc } from 'drizzle-orm';
import { nanoid } from 'nanoid';
import { requireAdmin, ok, err } from '@/lib/admin-api';
import { getManualSource } from '@/lib/sources';
import { recordManualOverride, type Level } from '@/lib/quorum';
import { snapshotComponent, notifyForComponent, notifyIncident } from '@/lib/notify';

export const GET: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  const id = ctx.params.id!;
  const rows = await db.select().from(incidents).where(eq(incidents.id, id));
  if (!rows[0]) return err('not_found', 'Incident not found.', 404);
  const timeline = await db.select().from(incidentTimeline)
    .where(eq(incidentTimeline.incidentId, id)).orderBy(desc(incidentTimeline.at));
  return ok({ ...rows[0], timeline });
};

export const PATCH: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  const id = ctx.params.id!;
  const rows = await db.select().from(incidents).where(eq(incidents.id, id));
  const inc = rows[0];
  if (!inc) return err('not_found', 'Incident not found.', 404);

  const body = await ctx.request.json().catch(() => null);
  if (!body) return err('bad_request', 'Invalid JSON body.', 400);

  const now = new Date();

  // Level override: an engine concern. Re-assert through the engine so the read
  // model agrees and the incident is marked human-owned.
  if (body.severity && body.severity !== inc.severity) {
    const level: Level = body.severity === 'major' ? 'major' : 'degraded';
    const manual = await getManualSource();
    const before = await snapshotComponent(inc.affects[0]);
    await recordManualOverride({
      manualSourceId: manual.id,
      componentId: inc.affects[0],
      signal: level === 'major' ? 'down' : 'degraded',
      level,
      body: body.note || `Level overridden to ${body.severity} by ${who}.`,
      author: who,
      now,
    });
    await notifyForComponent(inc.affects[0], before);
  }

  // Status move: human-owned narration. Write field + a timeline entry.
  if (body.status && body.status !== inc.status) {
    if (body.status === 'resolved') {
      // Force-resolve through the engine (manual 'ok' observation), NOT a bare
      // status flip — otherwise the next sweep sees a live manual non-ok read
      // and re-opens a zombie auto-incident. Mirrors resolve.ts.
      const manual = await getManualSource();
      const before = await snapshotComponent(inc.affects[0]);
      await recordManualOverride({
        manualSourceId: manual.id,
        componentId: inc.affects[0],
        signal: 'ok',
        body: body.note || `Resolved by ${who}.`,
        author: who,
        now,
      });
      await notifyForComponent(inc.affects[0], before);
    } else {
      await db.update(incidents).set({ status: body.status }).where(eq(incidents.id, id));
      await db.insert(incidentTimeline).values({
        id: nanoid(), incidentId: id, at: now,
        label: String(body.status).toUpperCase(),
        body: body.note || `Status moved to ${body.status}.`,
        author: who,
      });
      // Narrate the human status move.
      await notifyIncident(id, 'update');
    }
  }

  // Plain title / summary edits (narration metadata).
  const meta: Record<string, unknown> = {};
  if (body.title) meta.title = body.title;
  if (body.summary) meta.summary = body.summary;
  if (Object.keys(meta).length) await db.update(incidents).set(meta).where(eq(incidents.id, id));

  const updated = await db.select().from(incidents).where(eq(incidents.id, id));
  return ok(updated[0]);
};
