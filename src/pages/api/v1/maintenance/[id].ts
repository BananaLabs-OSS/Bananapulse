import type { APIRoute } from 'astro';
import { db } from '@/db';
import { maintenance } from '@/db/schema';
import { validateApiToken } from '@/lib/api-tokens';
import { componentExists, isLeafComponent } from '@/lib/components';
import { eq } from 'drizzle-orm';
import { pulpOwnerRouteFamilyConfigured } from '@/lib/pulp-bridge';
import {
  ownerCommandID,
  ownerMaintenanceByID,
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

export const PATCH: APIRoute = async ({ request, params }) => {
  const token = await authenticate(request, 'write');
  if (!token) return new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'Invalid or insufficient API token.' } }), { status: 401 });
  if (pulpOwnerRouteFamilyConfigured('maintenance')) {
    const existing = await ownerMaintenanceByID(params.id!);
    if (!existing || existing.cancelled) return new Response(JSON.stringify({ error: { code: 'not_found', message: 'Maintenance not found.' } }), { status: 404 });
    const body = await request.json();
    const patch: NonNullable<Parameters<typeof sendOwnerMaintenanceCommand>[0]['maintenance_patch']> = { id: params.id! };
    if (body.title !== undefined) patch.title = body.title;
    if (body.summary !== undefined) patch.summary = body.summary;
    if (body.scheduled_start !== undefined) {
      const date = new Date(body.scheduled_start);
      if (Number.isNaN(date.getTime())) return new Response(JSON.stringify({ error: { code: 'bad_request', message: 'scheduled_start is not a valid date.' } }), { status: 400 });
      patch.scheduled_start_unix = toUnixSeconds(date);
    }
    if (body.scheduled_end !== undefined) {
      const date = new Date(body.scheduled_end);
      if (Number.isNaN(date.getTime())) return new Response(JSON.stringify({ error: { code: 'bad_request', message: 'scheduled_end is not a valid date.' } }), { status: 400 });
      patch.scheduled_end_unix = toUnixSeconds(date);
    }
    const effectiveStart = patch.scheduled_start_unix ?? existing.scheduled_start_unix;
    const effectiveEnd = patch.scheduled_end_unix ?? existing.scheduled_end_unix;
    if (effectiveStart >= effectiveEnd) return new Response(JSON.stringify({ error: { code: 'bad_request', message: 'scheduled_start must be before scheduled_end.' } }), { status: 400 });
    if (body.affects !== undefined) {
      if (!Array.isArray(body.affects) || !body.affects.length) return new Response(JSON.stringify({ error: { code: 'bad_request', message: 'affects must be a non-empty array.' } }), { status: 400 });
      const validation = await validateOwnerAffects(body.affects, (id) => `Component "${id}" is not a leaf (declare on a service or host).`);
      if (validation) return new Response(JSON.stringify({ error: { code: 'bad_request', message: validation } }), { status: 400 });
      patch.affects = body.affects;
    }
    await sendOwnerMaintenanceCommand({
      version: 'monitor.v1', id: ownerCommandID('maintenance-edit', `${params.id}\0${JSON.stringify(body)}`),
      kind: 'edit_maintenance', at_unix: ownerNowUnix(), maintenance_patch: patch,
    });
    return new Response(JSON.stringify({ data: toLegacyMaintenance({
      ...existing,
      ...patch,
      scheduled_start_unix: effectiveStart,
      scheduled_end_unix: effectiveEnd,
      affects: patch.affects ?? existing.affects,
    }) }), { headers: { 'Content-Type': 'application/json' } });
  }
  const rows = await db.select().from(maintenance).where(eq(maintenance.id, params.id!));
  const row = rows[0];
  if (!row) return new Response(JSON.stringify({ error: { code: 'not_found', message: 'Maintenance not found.' } }), { status: 404 });

  const body = await request.json();
  const updates: Record<string, any> = {};
  if (body.title !== undefined) updates.title = body.title;
  if (body.summary !== undefined) updates.summary = body.summary;

  if (body.scheduled_start !== undefined) {
    const d = new Date(body.scheduled_start);
    if (isNaN(d.getTime())) {
      return new Response(JSON.stringify({ error: { code: 'bad_request', message: 'scheduled_start is not a valid date.' } }), { status: 400 });
    }
    updates.scheduledStart = d;
  }
  if (body.scheduled_end !== undefined) {
    const d = new Date(body.scheduled_end);
    if (isNaN(d.getTime())) {
      return new Response(JSON.stringify({ error: { code: 'bad_request', message: 'scheduled_end is not a valid date.' } }), { status: 400 });
    }
    updates.scheduledEnd = d;
  }
  // start must precede end (using existing row values for any side not changed).
  const effStart = (updates.scheduledStart ?? row.scheduledStart) as Date;
  const effEnd = (updates.scheduledEnd ?? row.scheduledEnd) as Date;
  if (effStart && effEnd && effStart.getTime() >= effEnd.getTime()) {
    return new Response(JSON.stringify({ error: { code: 'bad_request', message: 'scheduled_start must be before scheduled_end.' } }), { status: 400 });
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
    updates.affects = body.affects;
  }

  await db.update(maintenance).set(updates).where(eq(maintenance.id, params.id!));
  const updated = await db.select().from(maintenance).where(eq(maintenance.id, params.id!));
  return new Response(JSON.stringify({ data: updated[0] }), { headers: { 'Content-Type': 'application/json' } });
};

export const DELETE: APIRoute = async ({ request, params }) => {
  const token = await authenticate(request, 'full');
  if (!token) return new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'Invalid or insufficient API token.' } }), { status: 401 });
  if (pulpOwnerRouteFamilyConfigured('maintenance')) {
    // Legacy DELETE is deliberately idempotent: deleting an absent row still
    // returns { deleted: true }. Avoid dispatching a rejected owner command.
    const existing = await ownerMaintenanceByID(params.id!);
    if (existing && !existing.cancelled) {
      await sendOwnerMaintenanceCommand({
        version: 'monitor.v1', id: ownerCommandID('maintenance-delete', params.id!),
        kind: 'delete_maintenance', at_unix: ownerNowUnix(), maintenance_id: params.id!,
      });
    }
    return new Response(JSON.stringify({ data: { deleted: true } }), { headers: { 'Content-Type': 'application/json' } });
  }
  await db.delete(maintenance).where(eq(maintenance.id, params.id!));
  return new Response(JSON.stringify({ data: { deleted: true } }), { headers: { 'Content-Type': 'application/json' } });
};
