/**
 * Admin incident endpoints (admin-session auth).
 *
 *   GET  /api/v1/admin/incidents          list incidents
 *   POST /api/v1/admin/incidents          MANUAL DECLARE — the engine path
 *
 * A manual declare does NOT hand-write incident status fields. It calls
 * recordManualOverride, which appends a high-weight `manual`-source
 * observation and opens a human-owned (auto=false) incident through the SAME
 * quorum engine the monitors use. The engine, not this route, owns existence.
 */
import type { APIRoute } from 'astro';
import { db } from '@/db';
import { incidents } from '@/db/schema';
import { desc } from 'drizzle-orm';
import { requireAdmin, ok, err } from '@/lib/admin-api';
import { getManualSource } from '@/lib/sources';
import { recordManualOverride, openIncidentFor, type Level } from '@/lib/quorum';
import { snapshotComponent, notifyForComponent } from '@/lib/notify';
import { componentExists, isLeafComponent } from '@/lib/components';

export const GET: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  const rows = await db.select().from(incidents).orderBy(desc(incidents.startedAt)).limit(100);
  return ok(rows);
};

export const POST: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;

  const body = await ctx.request.json().catch(() => null);
  if (!body) return err('bad_request', 'Invalid JSON body.', 400);

  // Every affected id must resolve to a real LEAF component (service/host), or
  // the incident would be invisible on the public surface (the invisible-outage
  // bug) / declared up the tree where status is derived, not observed.
  const affects: string[] = Array.isArray(body.affects)
    ? body.affects
    : (body.componentId ? [body.componentId] : []);
  if (!affects.length) return err('bad_request', 'A componentId (affected component) is required.', 400);
  for (const a of affects) {
    if (!(await componentExists(a))) return err('bad_request', `Unknown component "${a}".`, 400);
    if (!(await isLeafComponent(a))) return err('bad_request', `Component "${a}" is not a leaf (declare on a service or host).`, 400);
  }

  // severity -> engine level. 'major' is the only major; everything else degraded.
  const level: Level = body.severity === 'major' ? 'major' : 'degraded';
  const signal = level === 'major' ? 'down' : 'degraded';
  const summary: string = body.summary || body.title || 'Investigating an issue.';

  const manual = await getManualSource();
  // Declare for EVERY validated affected component via the engine path, so a
  // multi-component declare records all of them (not just affects[0]).
  for (const componentId of affects) {
    const before = await snapshotComponent(componentId);
    // Flows through the same engine as a monitor observation (high-weight manual source).
    await recordManualOverride({
      manualSourceId: manual.id,
      componentId,
      signal,
      level,
      body: summary,
      title: body.title || undefined,
      author: who,
    });
    await notifyForComponent(componentId, before);
  }

  const open = await openIncidentFor(affects[0]);
  return ok(open, 201);
};
