import type { APIRoute } from 'astro';
import { db } from '@/db';
import { incidents, incidentTimeline } from '@/db/schema';
import { requireApiToken } from '@/lib/api-tokens';
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
  await db.insert(incidentTimeline).values({
    id,
    incidentId: params.id!,
    at: new Date(),
    label: label.toUpperCase(),
    body: entryBody,
  });

  return new Response(JSON.stringify({ data: { id, incidentId: params.id, label: label.toUpperCase(), body: entryBody } }), { status: 201, headers: { 'Content-Type': 'application/json' } });
};
