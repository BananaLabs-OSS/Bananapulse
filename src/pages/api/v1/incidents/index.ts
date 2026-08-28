import type { APIRoute } from 'astro';
import { db } from '@/db';
import { incidents, incidentTimeline } from '@/db/schema';
import { requireApiToken } from '@/lib/api-tokens';
import { componentExists, isLeafComponent } from '@/lib/components';
import { eq, desc, and, arrayContains } from 'drizzle-orm';
import { nanoid } from 'nanoid';
import { pulpOwnerRouteFamilyConfigured } from '@/lib/pulp-bridge';
import {
  incidentProjection,
  legacyIncident,
  newOwnerCommand,
  ownerFailure,
  sendIncidentCommand,
} from './owner';

const VALID_SEVERITY = ['minor', 'moderate', 'major'];
const VALID_STATUS = ['investigating', 'identified', 'monitoring', 'resolved'];

export const GET: APIRoute = async ({ request, url }) => {
  const token = await requireApiToken(request, 'read');
  if (!token) return new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'Invalid or insufficient API token.' } }), { status: 401 });

  const status = url.searchParams.get('status');
  const product = url.searchParams.get('product');
  const limit = parseInt(url.searchParams.get('limit') ?? '50');
  const offset = parseInt(url.searchParams.get('offset') ?? '0');

  if (pulpOwnerRouteFamilyConfigured('incidents')) {
    try {
      const projection = await incidentProjection();
      let rows = (projection.incidents ?? []).map(legacyIncident);
      if (status) rows = rows.filter((incident) => incident.status === status);
      if (product) rows = rows.filter((incident) => incident.affects.includes(product));
      rows.sort((left, right) => Date.parse(right.startedAt ?? '') - Date.parse(left.startedAt ?? ''));
      return new Response(JSON.stringify({ data: rows.slice(offset, offset + limit) }), {
        headers: { 'Content-Type': 'application/json' },
      });
    } catch (error) {
      return ownerFailure(error);
    }
  }

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

  if (pulpOwnerRouteFamilyConfigured('incidents')) {
    const id = nanoid();
    const now = Math.floor(Date.now() / 1_000);
    const command = newOwnerCommand('open', id, {
      at_unix: now,
      incident: {
        id,
        title,
        summary,
        severity,
        affects,
        status: incStatus ?? 'investigating',
        auto: false,
        started_at_unix: now,
        created_at_unix: now,
      },
    });
    // The old public API creates the row only; it deliberately does not page
    // subscribers. Keep that behavioral boundary while moving its state owner.
    try {
      await sendIncidentCommand(command);
      const created = await ownerIncidentForResponse(id);
      if (!created) return ownerFailure(new Error('owner did not return created incident'));
      return new Response(JSON.stringify({ data: created }), { status: 201, headers: { 'Content-Type': 'application/json' } });
    } catch (error) {
      return ownerFailure(error);
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

async function ownerIncidentForResponse(id: string) {
  const projection = await incidentProjection();
  const incident = projection.incidents?.find((candidate) => candidate.id === id);
  return incident ? legacyIncident(incident) : null;
}
