import type { APIRoute } from 'astro';
import { db } from '@/db';
import { incidents, incidentTimeline } from '@/db/schema';
import { requireApiToken } from '@/lib/api-tokens';
import { componentExists, isLeafComponent } from '@/lib/components';
import { eq, desc, and, arrayContains } from 'drizzle-orm';
import { nanoid } from 'nanoid';

const VALID_SEVERITY = ['minor', 'moderate', 'major'];
const VALID_STATUS = ['investigating', 'identified', 'monitoring', 'resolved'];

export const GET: APIRoute = async ({ request, url }) => {
  const token = await requireApiToken(request, 'read');
  if (!token) return new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'Invalid or insufficient API token.' } }), { status: 401 });

  const status = url.searchParams.get('status');
  const product = url.searchParams.get('product');
  const limit = parseInt(url.searchParams.get('limit') ?? '50');
  const offset = parseInt(url.searchParams.get('offset') ?? '0');

  const conditions = [];
  if (status) conditions.push(eq(incidents.status, status));
  if (product) conditions.push(arrayContains(incidents.affects, [product]));

  const rows = await db.select().from(incidents)
    .where(conditions.length ? and(...conditions) : undefined)
    .orderBy(desc(incidents.startedAt))
    .limit(limit).offset(offset);

  return new Response(JSON.stringify({ data: rows }), { headers: { 'Content-Type': 'application/json' } });
};

export const POST: APIRoute = async ({ request }) => {
  const token = await requireApiToken(request, 'write');
  if (!token) return new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'Invalid or insufficient API token.' } }), { status: 401 });

  const body = await request.json();
  const { title, summary, severity, affects, status: incStatus } = body;
  if (!title || !summary || !severity || !affects?.length) {
    return new Response(JSON.stringify({ error: { code: 'bad_request', message: 'title, summary, severity, and affects are required.' } }), { status: 400 });
  }
  if (!VALID_SEVERITY.includes(severity)) {
    return new Response(JSON.stringify({ error: { code: 'bad_request', message: `severity must be one of ${VALID_SEVERITY.join(', ')}.` } }), { status: 400 });
  }
  if (incStatus != null && !VALID_STATUS.includes(incStatus)) {
    return new Response(JSON.stringify({ error: { code: 'bad_request', message: `status must be one of ${VALID_STATUS.join(', ')}.` } }), { status: 400 });
  }
  // Every affected id must resolve to a real LEAF component (service/host), or
  // the incident is invisible / declared up the tree.
  for (const a of affects) {
    if (!(await componentExists(a))) {
      return new Response(JSON.stringify({ error: { code: 'bad_request', message: `Unknown component "${a}".` } }), { status: 400 });
    }
    if (!(await isLeafComponent(a))) {
      return new Response(JSON.stringify({ error: { code: 'bad_request', message: `Component "${a}" is not a leaf (declare on a service or host).` } }), { status: 400 });
    }
  }

  const id = nanoid();
  await db.insert(incidents).values({
    id, title, summary, severity, affects,
    status: incStatus ?? 'investigating',
    startedAt: new Date(),
  });

  // Create initial timeline entry if provided
  if (body.initial_note) {
    await db.insert(incidentTimeline).values({
      id: nanoid(),
      incidentId: id,
      at: new Date(),
      label: (incStatus ?? 'INVESTIGATING').toUpperCase(),
      body: body.initial_note,
    });
  }

  const created = await db.select().from(incidents).where(eq(incidents.id, id));
  return new Response(JSON.stringify({ data: created[0] }), { status: 201, headers: { 'Content-Type': 'application/json' } });
};
