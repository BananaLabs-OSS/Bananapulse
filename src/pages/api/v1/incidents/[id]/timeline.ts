import type { APIRoute } from 'astro';
import { db } from '@/db';
import { incidents, incidentTimeline } from '@/db/schema';
import { requireApiToken } from '@/lib/api-tokens';
import { notifyIncident } from '@/lib/notify';
import { eq } from 'drizzle-orm';
import { nanoid } from 'nanoid';

export const POST: APIRoute = async ({ request, params }) => {
  const token = await requireApiToken(request, 'write');
  if (!token) return new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'Invalid or insufficient API token.' } }), { status: 401 });

  const body = await request.json();
  const { label, body: entryBody } = body;
  if (!label || !entryBody) {
    return new Response(JSON.stringify({ error: { code: 'bad_request', message: 'label and body are required.' } }), { status: 400 });
  }

  // The incident must exist — otherwise this orphans a timeline row (and feeds
  // the notify/latestUpdateBody path for a non-existent incident).
  const inc = await db.select({ id: incidents.id }).from(incidents).where(eq(incidents.id, params.id!));
  if (!inc[0]) {
    return new Response(JSON.stringify({ error: { code: 'not_found', message: 'Incident not found.' } }), { status: 404 });
  }

  const id = nanoid();
  const upperLabel = label.toUpperCase();
  await db.insert(incidentTimeline).values({
    id,
    incidentId: params.id!,
    at: new Date(),
    label: upperLabel,
    body: entryBody,
  });
  // Fan out to subscribers, mirroring the admin /updates path.
  await notifyIncident(params.id!, upperLabel === 'RESOLVED' ? 'resolved' : 'update');

  return new Response(JSON.stringify({ data: { id, incidentId: params.id, label: upperLabel, body: entryBody } }), { status: 201, headers: { 'Content-Type': 'application/json' } });
};
