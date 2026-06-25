/**
 * DB-backed incident queries for public pages.
 * Returns data mapped to the Incident / TimelineEntry interfaces
 * from types.ts so existing components work unchanged.
 */
import { db } from '@/db';
import { incidents, incidentTimeline } from '@/db/schema';
import { eq, ne, desc } from 'drizzle-orm';
import { loadComponentTree } from './db-components';
import type { Incident, TimelineEntry, IncidentSeverity, IncidentStatus } from './types';
import { UMBRELLA_ID } from '@/pulse.config';

/* ── helpers ────────────────────────────────────────────────── */

function mapDbIncident(row: typeof incidents.$inferSelect, timeline: TimelineEntry[], product: string): Incident {
  return {
    id: row.id,
    title: row.title,
    severity: row.severity as IncidentSeverity,
    status: row.status as IncidentStatus,
    product,
    affects: row.affects,
    auto: row.auto,
    started: row.startedAt.toISOString(),
    resolved: row.resolvedAt?.toISOString(),
    timeline,
  };
}

/** Walk a component up to its nearest product/organization ancestor id. */
function ancestorProduct(byId: Map<string, { id: string; parentId: string | null; kind: string }>, id: string): string {
  let cur = byId.get(id);
  let orgFallback: string | null = null;
  while (cur) {
    if (cur.kind === 'product') return cur.id;
    if (cur.kind === 'organization') orgFallback = cur.id;
    cur = cur.parentId ? byId.get(cur.parentId) : undefined;
  }
  return orgFallback ?? UMBRELLA_ID;
}

/** Resolve the product for an incident by walking the first affected component up the tree. */
async function resolveProduct(affects: string[]): Promise<string> {
  if (affects.length === 0) return UMBRELLA_ID;
  const byId = await loadComponentTree();
  return ancestorProduct(byId, affects[0]);
}

/** Build timeline entries for an incident. */
async function buildTimeline(incidentId: string): Promise<TimelineEntry[]> {
  const rows = await db.select().from(incidentTimeline)
    .where(eq(incidentTimeline.incidentId, incidentId))
    .orderBy(desc(incidentTimeline.at));
  return rows.map(r => ({
    status: r.label.toLowerCase() as IncidentStatus,
    body: r.body,
    timestamp: r.at.toISOString(),
  }));
}

/** All component ids in a product's subtree (the product node + every descendant). */
async function serviceIdsForProduct(product: string): Promise<string[]> {
  const byId = await loadComponentTree();
  const kids = new Map<string, string[]>();
  for (const r of byId.values()) {
    if (!r.parentId) continue;
    (kids.get(r.parentId) ?? kids.set(r.parentId, []).get(r.parentId)!).push(r.id);
  }
  const out: string[] = [];
  const walk = (id: string) => { out.push(id); for (const k of kids.get(id) ?? []) walk(k); };
  if (byId.has(product)) walk(product);
  return out;
}

function affectsProduct(affects: string[], productServiceIds: string[]): boolean {
  return affects.some(a => productServiceIds.includes(a));
}

function affectsService(affects: string[], serviceId: string): boolean {
  return affects.includes(serviceId);
}

/* ── public API ─────────────────────────────────────────────── */

export async function getActiveIncidents(product?: string, service?: string): Promise<Incident[]> {
  const rows = await db.select().from(incidents)
    .where(ne(incidents.status, 'resolved'))
    .orderBy(desc(incidents.startedAt));

  let filtered = rows;
  if (product) {
    const svcIds = await serviceIdsForProduct(product);
    filtered = filtered.filter(r => affectsProduct(r.affects, svcIds));
  }
  if (service) {
    filtered = filtered.filter(r => affectsService(r.affects, service));
  }

  return Promise.all(filtered.map(async (row) => {
    const tl = await buildTimeline(row.id);
    const prod = product ?? await resolveProduct(row.affects);
    return mapDbIncident(row, tl, prod);
  }));
}

export async function getResolvedIncidents(opts?: {
  product?: string;
  service?: string;
  limit?: number;
  offset?: number;
  noTimeline?: boolean; // history LISTS don't need per-incident timelines — skip the N extra queries
}): Promise<Incident[]> {
  const rows = await db.select().from(incidents)
    .where(eq(incidents.status, 'resolved'))
    .orderBy(desc(incidents.startedAt));

  let filtered = rows;
  if (opts?.product) {
    const svcIds = await serviceIdsForProduct(opts.product);
    filtered = filtered.filter(r => affectsProduct(r.affects, svcIds));
  }
  if (opts?.service) {
    filtered = filtered.filter(r => affectsService(r.affects, opts.service!));
  }

  const limit = opts?.limit ?? 20;
  const offset = opts?.offset ?? 0;
  const paged = filtered.slice(offset, offset + limit);

  return Promise.all(paged.map(async (row) => {
    const tl = opts?.noTimeline ? [] : await buildTimeline(row.id);
    const prod = opts?.product ?? (opts?.noTimeline ? '' : await resolveProduct(row.affects));
    return mapDbIncident(row, tl, prod);
  }));
}

export async function getAllIncidents(opts?: {
  product?: string;
  service?: string;
  limit?: number;
  offset?: number;
}): Promise<Incident[]> {
  const rows = await db.select().from(incidents)
    .orderBy(desc(incidents.startedAt));

  let filtered = rows;
  if (opts?.product) {
    const svcIds = await serviceIdsForProduct(opts.product);
    filtered = filtered.filter(r => affectsProduct(r.affects, svcIds));
  }
  if (opts?.service) {
    filtered = filtered.filter(r => affectsService(r.affects, opts.service!));
  }

  const limit = opts?.limit ?? 50;
  const offset = opts?.offset ?? 0;
  const paged = filtered.slice(offset, offset + limit);

  return Promise.all(paged.map(async (row) => {
    const tl = await buildTimeline(row.id);
    const prod = opts?.product ?? await resolveProduct(row.affects);
    return mapDbIncident(row, tl, prod);
  }));
}

export async function getIncident(id: string): Promise<Incident | undefined> {
  const rows = await db.select().from(incidents).where(eq(incidents.id, id));
  const row = rows[0];
  if (!row) return undefined;

  const tl = await buildTimeline(row.id);
  const prod = await resolveProduct(row.affects);
  return mapDbIncident(row, tl, prod);
}

export async function countAllIncidents(opts?: {
  product?: string;
  service?: string;
}): Promise<number> {
  const rows = await db.select().from(incidents)
    .orderBy(desc(incidents.startedAt));

  let filtered = rows;
  if (opts?.product) {
    const svcIds = await serviceIdsForProduct(opts.product);
    filtered = filtered.filter(r => affectsProduct(r.affects, svcIds));
  }
  if (opts?.service) {
    filtered = filtered.filter(r => affectsService(r.affects, opts.service!));
  }
  return filtered.length;
}
