/**
 * DB-backed maintenance queries for public pages.
 * Returns data mapped to the Maintenance interface from types.ts.
 */
import { db } from '@/db';
import { maintenance, components } from '@/db/schema';
import { gte, asc, isNull } from 'drizzle-orm';
import type { Maintenance } from './types';
import { UMBRELLA_ID } from '@/pulse.config';

function mapDbMaintenance(row: typeof maintenance.$inferSelect, product: string): Maintenance {
  return {
    id: row.id,
    title: row.title,
    product,
    scheduledStart: row.scheduledStart.toISOString(),
    scheduledEnd: row.scheduledEnd.toISOString(),
    body: row.summary,
  };
}

/** Load the active component tree once as a parent lookup (same model as the public surface). */
async function loadComponentTree() {
  const rows = await db.select({ id: components.id, parentId: components.parentId, kind: components.kind })
    .from(components).where(isNull(components.archivedAt));
  return new Map(rows.map((r) => [r.id, r]));
}

/** Resolve the product for a maintenance window by walking the first affected component up the tree. */
async function resolveProduct(affects: string[]): Promise<string> {
  if (affects.length === 0) return UMBRELLA_ID;
  const byId = await loadComponentTree();
  let cur = byId.get(affects[0]);
  let orgFallback: string | null = null;
  while (cur) {
    if (cur.kind === 'product') return cur.id;
    if (cur.kind === 'organization') orgFallback = cur.id;
    cur = cur.parentId ? byId.get(cur.parentId) : undefined;
  }
  return orgFallback ?? UMBRELLA_ID;
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

export async function getUpcomingMaintenance(product?: string): Promise<Maintenance[]> {
  const rows = await db.select().from(maintenance)
    .where(gte(maintenance.scheduledEnd, new Date()))
    .orderBy(asc(maintenance.scheduledStart));

  let filtered = rows;
  if (product) {
    const svcIds = await serviceIdsForProduct(product);
    filtered = filtered.filter(r => r.affects.some(a => svcIds.includes(a)));
  }

  return Promise.all(filtered.map(async (row) => {
    const prod = product ?? await resolveProduct(row.affects);
    return mapDbMaintenance(row, prod);
  }));
}
