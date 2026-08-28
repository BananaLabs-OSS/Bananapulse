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
import { pulpOwnerRouteFamilyConfigured } from '@/lib/pulp-bridge';
import {
  legacyIncident,
  legacyTimeline,
  newOwnerCommand,
  ownerFailure,
  ownerIncident,
  publishIncidentCommand,
  sendIncidentCommand,
} from '../../incidents/owner';

export const GET: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  const id = ctx.params.id!;
  if (pulpOwnerRouteFamilyConfigured('incidents')) {
    try {
      const owned = await ownerIncident(id);
      if (!owned) return err('not_found', 'Incident not found.', 404);
      return ok({
        ...legacyIncident(owned.incident),
        timeline: owned.timeline.sort((left, right) => right.at_unix - left.at_unix).map(legacyTimeline),
      });
    } catch (error) {
      return ownerFailure(error);
    }
  }
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
  const ownerEnabled = pulpOwnerRouteFamilyConfigured('incidents');
  let owned: Awaited<ReturnType<typeof ownerIncident>> | null = null;
  if (ownerEnabled) {
    try {
      owned = await ownerIncident(id);
    } catch (error) {
      return ownerFailure(error);
    }
    if (!owned) return err('not_found', 'Incident not found.', 404);
  }
  const body = await ctx.request.json().catch(() => null);
  if (!body) return err('bad_request', 'Invalid JSON body.', 400);

  if (ownerEnabled && owned) {
    try {
      const now = Math.floor(Date.now() / 1_000);
      const statusChanged = Boolean(body.status && body.status !== owned.incident.status);
      const severityChanged = Boolean(body.severity && body.severity !== owned.incident.severity);
      const nextSeverity = body.severity === 'major' ? 'major' : 'moderate';

      if (body.status === 'resolved' && statusChanged) {
        const command = newOwnerCommand('resolve', `${id}:resolve:${JSON.stringify(body)}`, {
          at_unix: now, incident: { id },
        });
        if (['moderate', 'major'].includes(owned.incident.severity)) {
          await publishIncidentCommand(command, {
            eventId: `${id}:resolved:${command.id}`,
            subject: `Resolved: ${owned.incident.title}`,
            body: body.note || `Resolved by ${who}.`,
          });
        } else {
          await sendIncidentCommand(command);
        }
      }

      const patch: Record<string, unknown> = { id, at_unix: now, author: who };
      if (body.severity && severityChanged) patch.severity = nextSeverity;
      if (body.status && body.status !== 'resolved' && statusChanged) {
        patch.status = body.status;
        patch.note = body.note || `Status moved to ${body.status}.`;
      }
      if (body.title) patch.title = body.title;
      if (body.summary) patch.summary = body.summary;
      if (Object.keys(patch).length > 3) {
        const command = newOwnerCommand('edit', `${id}:edit:${JSON.stringify(body)}`, {
          at_unix: now, incident_patch: patch,
        });
        if (statusChanged && body.status !== 'resolved') {
          await publishIncidentCommand(command, {
            eventId: `${id}:status:${command.id}`,
            subject: `Update: ${body.title || owned.incident.title}`,
            body: body.note || `Status moved to ${body.status}.`,
          });
        } else if (severityChanged && ['moderate', 'major'].includes(nextSeverity)) {
          await publishIncidentCommand(command, {
            eventId: `${id}:severity:${command.id}`,
            subject: `Update: ${body.title || owned.incident.title}`,
            body: body.note || `Level overridden to ${body.severity} by ${who}.`,
          });
        } else {
          await sendIncidentCommand(command);
        }
      }
      const updated = await ownerIncident(id);
      if (!updated) return ownerFailure(new Error('owner did not return updated incident'));
      return ok(legacyIncident(updated.incident));
    } catch (error) {
      return ownerFailure(error);
    }
  }

  // The selected owner path returns above. This keeps the fallback's incident
  // row non-null without ever letting an owner request fall back to Postgres.
  if (ownerEnabled) return ownerFailure(new Error('owner incident lookup was incomplete'));
  const rows = await db.select().from(incidents).where(eq(incidents.id, id));
  const inc = rows[0];
  if (!inc) return err('not_found', 'Incident not found.', 404);

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
      // and re-opens a zombie auto-incident. Mirrors resolve.ts and the public PATCH.
      // Loop over ALL affected components so multi-component incidents are fully
      // cleared and don't leave zombie re-open state on the next sweep.
      const manual = await getManualSource();
      for (const componentId of inc.affects) {
        const before = await snapshotComponent(componentId);
        await recordManualOverride({
          manualSourceId: manual.id,
          componentId,
          signal: 'ok',
          body: body.note || `Resolved by ${who}.`,
          author: who,
          now,
        });
        await notifyForComponent(componentId, before);
      }
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
