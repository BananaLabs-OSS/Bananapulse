import type { APIRoute } from 'astro';
import { db } from '@/db';
import { incidents } from '@/db/schema';
import { validateApiToken } from '@/lib/api-tokens';
import { componentExists, isLeafComponent } from '@/lib/components';
import { eq, desc, and, arrayContains } from 'drizzle-orm';
import { getManualSource } from '@/lib/sources';
import { recordManualOverride, openIncidentFor, type Level } from '@/lib/quorum';
import { snapshotComponent, notifyForComponent } from '@/lib/notify';

const VALID_SEVERITY = ['minor', 'moderate', 'major'];

async function authenticate(request: Request, requiredScope: 'read' | 'write' | 'full') {
  const auth = request.headers.get('Authorization');
  if (!auth?.startsWith('Bearer ')) return null;
  const token = await validateApiToken(auth.slice(7));
  if (!token) return null;
  const scopeRank: Record<string, number> = { read: 1, write: 2, full: 3 };
  if ((scopeRank[token.scope] ?? 0) < (scopeRank[requiredScope] ?? 3)) return null;
  return token;
}

export const GET: APIRoute = async ({ request, url }) => {
  const token = await authenticate(request, 'read');
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
  const token = await authenticate(request, 'write');
  if (!token) return new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'Invalid or insufficient API token.' } }), { status: 401 });

  const body = await request.json();
  const { title, summary, severity, affects } = body;
  if (!title || !summary || !severity || !affects?.length) {
    return new Response(JSON.stringify({ error: { code: 'bad_request', message: 'title, summary, severity, and affects are required.' } }), { status: 400 });
  }
  if (!VALID_SEVERITY.includes(severity)) {
    return new Response(JSON.stringify({ error: { code: 'bad_request', message: `severity must be one of ${VALID_SEVERITY.join(', ')}.` } }), { status: 400 });
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

  // Route through the same quorum engine the admin path uses so a token-auth
  // create cannot bypass quorum. Direct db.insert bypassed all engine logic.
  const level: Level = severity === 'major' ? 'major' : 'degraded';
  const signal = level === 'major' ? 'down' : 'degraded';
  const manual = await getManualSource();
  const author = `api-token:${token.id}`;
  for (const componentId of affects) {
    const before = await snapshotComponent(componentId);
    await recordManualOverride({
      manualSourceId: manual.id,
      componentId,
      signal,
      level,
      body: body.initial_note || summary,
      title: title || undefined,
      author,
    });
    await notifyForComponent(componentId, before);
  }

  const open = await openIncidentFor(affects[0]);
  return new Response(JSON.stringify({ data: open }), { status: 201, headers: { 'Content-Type': 'application/json' } });
};
