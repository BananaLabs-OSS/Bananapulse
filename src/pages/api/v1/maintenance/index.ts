import type { APIRoute } from 'astro';
import { db } from '@/db';
import { maintenance } from '@/db/schema';
import { validateApiToken } from '@/lib/api-tokens';
import { componentExists, isLeafComponent } from '@/lib/components';
import { eq, asc } from 'drizzle-orm';
import { nanoid } from 'nanoid';
import { pulpOwnerRouteFamilyConfigured } from '@/lib/pulp-bridge';
import {
  ownerCommandID,
  ownerMaintenanceRows,
  ownerNowUnix,
  sendOwnerMaintenanceCommand,
  toLegacyMaintenance,
  toUnixSeconds,
  validateOwnerAffects,
} from './owner';

async function authenticate(request: Request, requiredScope: 'read' | 'write' | 'full') {
  const auth = request.headers.get('Authorization');
  if (!auth?.startsWith('Bearer ')) return null;
  const token = await validateApiToken(auth.slice(7));
  if (!token) return null;
  const scopeRank: Record<string, number> = { read: 1, write: 2, full: 3 };
  if ((scopeRank[token.scope] ?? 0) < (scopeRank[requiredScope] ?? 3)) return null;
  return token;
}

export const GET: APIRoute = async ({ request }) => {
  const token = await authenticate(request, 'read');
  if (!token) return new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'Invalid or insufficient API token.' } }), { status: 401 });
  if (pulpOwnerRouteFamilyConfigured('maintenance')) {
    return new Response(JSON.stringify({ data: await ownerMaintenanceRows() }), { headers: { 'Content-Type': 'application/json' } });
  }
  const rows = await db.select().from(maintenance).orderBy(asc(maintenance.scheduledStart));
  return new Response(JSON.stringify({ data: rows }), { headers: { 'Content-Type': 'application/json' } });
};

export const POST: APIRoute = async ({ request }) => {
  const token = await authenticate(request, 'write');
  if (!token) return new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'Invalid or insufficient API token.' } }), { status: 401 });
  const body = await request.json();
  const { title, summary, scheduled_start, scheduled_end, affects } = body;
  if (!title || !summary || !scheduled_start || !scheduled_end || !affects?.length) {
    return new Response(JSON.stringify({ error: { code: 'bad_request', message: 'title, summary, scheduled_start, scheduled_end, and affects are required.' } }), { status: 400 });
  }
  if (pulpOwnerRouteFamilyConfigured('maintenance')) {
    const start = new Date(scheduled_start);
    const end = new Date(scheduled_end);
    // This endpoint historically accepted parseable dates here, while the
    // owner requires a valid, ordered Unix-second interval.
    if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime()) || start.getTime() >= end.getTime()) {
      return new Response(JSON.stringify({ error: { code: 'bad_request', message: 'Maintenance schedule is not valid.' } }), { status: 400 });
    }
    const validation = await validateOwnerAffects(affects, (id) => `Component "${id}" is not a leaf (schedule on a service or host).`);
    if (validation) return new Response(JSON.stringify({ error: { code: 'bad_request', message: validation } }), { status: 400 });
    const id = nanoid();
    const now = ownerNowUnix();
    await sendOwnerMaintenanceCommand({
      version: 'monitor.v1',
      id: ownerCommandID('maintenance-create', id),
      kind: 'schedule_maintenance',
      at_unix: now,
      maintenance: {
        id, title, summary, kind: 'scheduled',
        scheduled_start_unix: toUnixSeconds(start),
        scheduled_end_unix: toUnixSeconds(end),
        affects, created_at_unix: now,
      },
    });
    return new Response(JSON.stringify({ data: toLegacyMaintenance({
      id, title, summary, kind: 'scheduled',
      scheduled_start_unix: toUnixSeconds(start), scheduled_end_unix: toUnixSeconds(end),
      affects, created_at_unix: now,
    }) }), { status: 201, headers: { 'Content-Type': 'application/json' } });
  }
  // Every affected id must resolve to a real LEAF component, or the window
  // renders on no page (the invisible-maintenance bug).
  for (const a of affects) {
    if (!(await componentExists(a))) {
      return new Response(JSON.stringify({ error: { code: 'bad_request', message: `Unknown component "${a}".` } }), { status: 400 });
    }
    if (!(await isLeafComponent(a))) {
      return new Response(JSON.stringify({ error: { code: 'bad_request', message: `Component "${a}" is not a leaf (schedule on a service or host).` } }), { status: 400 });
    }
  }
  const id = nanoid();
  await db.insert(maintenance).values({ id, title, summary, scheduledStart: new Date(scheduled_start), scheduledEnd: new Date(scheduled_end), affects });
  const created = await db.select().from(maintenance).where(eq(maintenance.id, id));
  return new Response(JSON.stringify({ data: created[0] }), { status: 201, headers: { 'Content-Type': 'application/json' } });
};
