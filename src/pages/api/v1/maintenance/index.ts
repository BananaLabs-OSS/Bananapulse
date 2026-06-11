import type { APIRoute } from 'astro';
import { db } from '@/db';
import { maintenance } from '@/db/schema';
import { validateApiToken } from '@/lib/api-tokens';
import { componentExists, isLeafComponent } from '@/lib/components';
import { eq, asc } from 'drizzle-orm';
import { nanoid } from 'nanoid';

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
