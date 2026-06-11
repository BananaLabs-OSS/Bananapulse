import type { APIRoute } from 'astro';
import { db } from '@/db';
import { incidents, incidentTimeline } from '@/db/schema';
import { validateApiToken } from '@/lib/api-tokens';
import { componentExists, isLeafComponent } from '@/lib/components';
import { getManualSource } from '@/lib/sources';
import { recordManualOverride } from '@/lib/quorum';
import { snapshotComponent, notifyForComponent } from '@/lib/notify';
import { eq, desc } from 'drizzle-orm';

const VALID_SEVERITY = ['minor', 'moderate', 'major'];
const VALID_STATUS = ['investigating', 'identified', 'monitoring', 'resolved'];

async function authenticate(request: Request, requiredScope: 'read' | 'write' | 'full') {
  const auth = request.headers.get('Authorization');
  if (!auth?.startsWith('Bearer ')) return null;
  const token = await validateApiToken(auth.slice(7));
  if (!token) return null;
  const scopeRank: Record<string, number> = { read: 1, write: 2, full: 3 };
  if ((scopeRank[token.scope] ?? 0) < (scopeRank[requiredScope] ?? 3)) return null;
  return token;
}

export const GET: APIRoute = async ({ request, params }) => {
  const token = await authenticate(request, 'read');
  if (!token) return new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'Invalid or insufficient API token.' } }), { status: 401 });

  const rows = await db.select().from(incidents).where(eq(incidents.id, params.id!));
  if (!rows[0]) return new Response(JSON.stringify({ error: { code: 'not_found', message: 'Incident not found.' } }), { status: 404 });

  const timeline = await db.select().from(incidentTimeline)
    .where(eq(incidentTimeline.incidentId, params.id!))
    .orderBy(desc(incidentTimeline.at));

  return new Response(JSON.stringify({ data: { ...rows[0], timeline } }), { headers: { 'Content-Type': 'application/json' } });
};

export const PATCH: APIRoute = async ({ request, params }) => {
  const token = await authenticate(request, 'write');
  if (!token) return new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'Invalid or insufficient API token.' } }), { status: 401 });

  const rows = await db.select().from(incidents).where(eq(incidents.id, params.id!));
  const inc = rows[0];
  if (!inc) return new Response(JSON.stringify({ error: { code: 'not_found', message: 'Incident not found.' } }), { status: 404 });

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
  const token = await authenticate(request, 'full');
  if (!token) return new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'Invalid or insufficient API token.' } }), { status: 401 });

  await db.delete(incidents).where(eq(incidents.id, params.id!));
  return new Response(JSON.stringify({ data: { deleted: true } }), { headers: { 'Content-Type': 'application/json' } });
};
