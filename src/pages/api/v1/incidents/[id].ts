import type { APIRoute } from 'astro';
import { db } from '@/db';
import { incidents, incidentTimeline } from '@/db/schema';
import { requireApiToken } from '@/lib/api-tokens';
import { componentExists, isLeafComponent } from '@/lib/components';
import { getManualSource } from '@/lib/sources';
import { recordManualOverride } from '@/lib/quorum';
import { snapshotComponent, notifyForComponent } from '@/lib/notify';
import { eq, desc } from 'drizzle-orm';
import { pulpOwnerRouteFamilyConfigured } from '@/lib/pulp-bridge';
import {
  legacyIncident,
  legacyTimeline,
  newOwnerCommand,
  ownerFailure,
  ownerIncident,
  publishIncidentCommand,
  sendIncidentCommand,
} from './owner';

const VALID_SEVERITY = ['minor', 'moderate', 'major'];
const VALID_STATUS = ['investigating', 'identified', 'monitoring', 'resolved'];

export const GET: APIRoute = async ({ request, params }) => {
  const token = await requireApiToken(request, 'read');
  if (!token) return new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'Invalid or insufficient API token.' } }), { status: 401 });

  if (pulpOwnerRouteFamilyConfigured('incidents')) {
    try {
      const owned = await ownerIncident(params.id!);
      if (!owned) return new Response(JSON.stringify({ error: { code: 'not_found', message: 'Incident not found.' } }), { status: 404 });
      return new Response(JSON.stringify({ data: {
        ...legacyIncident(owned.incident),
        timeline: owned.timeline
          .sort((left, right) => right.at_unix - left.at_unix)
          .map(legacyTimeline),
      } }), { headers: { 'Content-Type': 'application/json' } });
    } catch (error) {
      return ownerFailure(error);
    }
  }

  const rows = await db.select().from(incidents).where(eq(incidents.id, params.id!));
  if (!rows[0]) return new Response(JSON.stringify({ error: { code: 'not_found', message: 'Incident not found.' } }), { status: 404 });

  const timeline = await db.select().from(incidentTimeline)
    .where(eq(incidentTimeline.incidentId, params.id!))
    .orderBy(desc(incidentTimeline.at));

  return new Response(JSON.stringify({ data: { ...rows[0], timeline } }), { headers: { 'Content-Type': 'application/json' } });
};

export const PATCH: APIRoute = async ({ request, params }) => {
  const token = await requireApiToken(request, 'write');
  if (!token) return new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'Invalid or insufficient API token.' } }), { status: 401 });

  const ownerEnabled = pulpOwnerRouteFamilyConfigured('incidents');
  let owned: Awaited<ReturnType<typeof ownerIncident>> | null = null;
  if (ownerEnabled) {
    try {
      owned = await ownerIncident(params.id!);
    } catch (error) {
      return ownerFailure(error);
    }
    if (!owned) return new Response(JSON.stringify({ error: { code: 'not_found', message: 'Incident not found.' } }), { status: 404 });
  }

  const body = await request.json();
  if (body.severity !== undefined && !VALID_SEVERITY.includes(body.severity)) {
    return new Response(JSON.stringify({ error: { code: 'bad_request', message: `severity must be one of ${VALID_SEVERITY.join(', ')}.` } }), { status: 400 });
  }
  if (body.status !== undefined && !VALID_STATUS.includes(body.status)) {
    return new Response(JSON.stringify({ error: { code: 'bad_request', message: `status must be one of ${VALID_STATUS.join(', ')}.` } }), { status: 400 });
  }
  if (body.affects !== undefined) {
    if (!Array.isArray(body.affects) || !body.affects.length) {
      return new Response(JSON.stringify({ error: { code: 'bad_request', message: 'affects must be a non-empty array.' } }), { status: 400 });
    }
    for (const a of body.affects) {
      if (!(await componentExists(a))) {
        return new Response(JSON.stringify({ error: { code: 'bad_request', message: `Unknown component "${a}".` } }), { status: 400 });
      }
      if (!(await isLeafComponent(a))) {
        return new Response(JSON.stringify({ error: { code: 'bad_request', message: `Component "${a}" is not a leaf (declare on a service or host).` } }), { status: 400 });
      }
    }
  }

  if (ownerEnabled && owned) {
    try {
      const now = Math.floor(Date.now() / 1_000);
      if (body.status === 'resolved') {
        const command = newOwnerCommand('resolve', `${params.id}:${JSON.stringify(body)}`, {
          at_unix: now,
          incident: { id: params.id },
        });
        // Legacy notification is conditional on a notable incident transition.
        // Keep that boundary, but make intent creation durable and sequenced by Lua.
        if (['moderate', 'major'].includes(owned.incident.severity)) {
          await publishIncidentCommand(command, {
            eventId: `${params.id}:resolved:${command.id}`,
            subject: `Resolved: ${owned.incident.title}`,
            body: body.note || 'Monitoring reports recovery.',
          });
        } else {
          await sendIncidentCommand(command);
        }
      }
      const patch: Record<string, unknown> = { id: params.id, at_unix: now, author: token.name ?? 'api' };
      if (body.status !== undefined && body.status !== 'resolved') patch.status = body.status;
      if (body.severity !== undefined) patch.severity = body.severity;
      if (body.affects !== undefined) patch.affects = body.affects;
      if (body.title !== undefined) patch.title = body.title;
      if (body.summary !== undefined) patch.summary = body.summary;
      if (Object.keys(patch).length > 3) {
        await sendIncidentCommand(newOwnerCommand('edit', `${params.id}:edit:${JSON.stringify(body)}`, {
          at_unix: now,
          incident_patch: patch,
        }));
      }
      const updated = await ownerIncident(params.id!);
      if (!updated) return ownerFailure(new Error('owner did not return updated incident'));
      return new Response(JSON.stringify({ data: legacyIncident(updated.incident) }), { headers: { 'Content-Type': 'application/json' } });
    } catch (error) {
      return ownerFailure(error);
    }
  }

  // The owner branch above is exhaustive whenever its feature flag is set.
  // Keeping this guard explicit gives the legacy path a non-null DB row.
  if (ownerEnabled) return ownerFailure(new Error('owner incident lookup was incomplete'));
  const rows = await db.select().from(incidents).where(eq(incidents.id, params.id!));
  const inc = rows[0];
  if (!inc) return new Response(JSON.stringify({ error: { code: 'not_found', message: 'Incident not found.' } }), { status: 404 });

  // Resolve must flow through the engine (manual 'ok' observation per affected
  // component), NOT a bare row flip — otherwise the next sweep sees the live
  // monitor/manual non-ok reads and re-opens a zombie auto-incident + re-emails.
  // Mirrors admin/incidents/[id].ts and resolve.ts.
  if (body.status === 'resolved') {
    const manual = await getManualSource();
    const now = new Date();
    for (const componentId of inc.affects) {
      const before = await snapshotComponent(componentId);
      await recordManualOverride({
        manualSourceId: manual.id,
        componentId,
        signal: 'ok',
        body: body.note || 'Resolved via API.',
        author: token.name ?? 'api',
        now,
      });
      await notifyForComponent(componentId, before);
    }
  }

  // Non-resolve field edits (status narration, severity, affects, metadata).
  const updates: Record<string, any> = {};
  if (body.status !== undefined && body.status !== 'resolved') updates.status = body.status;
  if (body.severity !== undefined) updates.severity = body.severity;
  if (body.affects !== undefined) updates.affects = body.affects;
  if (body.title !== undefined) updates.title = body.title;
  if (body.summary !== undefined) updates.summary = body.summary;
  if (Object.keys(updates).length) {
    await db.update(incidents).set(updates).where(eq(incidents.id, params.id!));
  }

  const updated = await db.select().from(incidents).where(eq(incidents.id, params.id!));
  return new Response(JSON.stringify({ data: updated[0] }), { headers: { 'Content-Type': 'application/json' } });
};

export const DELETE: APIRoute = async ({ request, params }) => {
  const token = await requireApiToken(request, 'full');
  if (!token) return new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'Invalid or insufficient API token.' } }), { status: 401 });

  if (pulpOwnerRouteFamilyConfigured('incidents')) {
    try {
      // Legacy DELETE is idempotent: deleting an absent row still returns true.
      const existing = await ownerIncident(params.id!);
      if (existing) {
        await sendIncidentCommand(newOwnerCommand('delete', params.id!, { incident_id: params.id }));
      }
      return new Response(JSON.stringify({ data: { deleted: true } }), { headers: { 'Content-Type': 'application/json' } });
    } catch (error) {
      return ownerFailure(error);
    }
  }

  await db.delete(incidents).where(eq(incidents.id, params.id!));
  return new Response(JSON.stringify({ data: { deleted: true } }), { headers: { 'Content-Type': 'application/json' } });
};
