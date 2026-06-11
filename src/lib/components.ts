/**
 * Component tree read layer (Banana Pulse model).
 *
 * One adjacency tree (organization → product → service → host). Builds the
 * ScopeView shape the existing skin consumes, for ANY node, at arbitrary depth.
 * Effective status bubbles up = worst of subtree, with the quorum engine's
 * derived status overlaid on observed leaves. URLs are relative to the scope's
 * landing root (the entry domain), so drilling never crosses domains.
 */
import { db } from '@/db';
import { components } from '@/db/schema';
import { isNull, asc, eq } from 'drizzle-orm';
import type { ServiceStatus, Incident } from './types';
import { statusToState } from './types';
import { derivedComponentStatuses, evaluateComponent, openIncidentFor } from './quorum';
import { getActiveIncidents } from './db-incidents';
import { rootComponentId, UMBRELLA_ID } from '@/pulse.config';
import type { ScopeView, ViewChild, CrumbItem } from './view';

type Comp = typeof components.$inferSelect;

function mapStatus(s: string): ServiceStatus {
  switch (s) {
    case 'ok': return 'operational';
    case 'deg': return 'degraded';
    case 'out': return 'outage';
    case 'maint': return 'maintenance';
    default: return 'operational';
  }
}
const WORST_ORDER: ServiceStatus[] = ['outage', 'degraded', 'maintenance', 'operational'];
function worst(a: ServiceStatus, b: ServiceStatus): ServiceStatus {
  for (const s of WORST_ORDER) if (a === s || b === s) return s;
  return 'operational';
}

/** A scope (entry domain) maps to its landing-root component (via the seam). */
export const scopeRootId = rootComponentId;

interface Tree {
  byId: Map<string, Comp>;
  kids: Map<string, Comp[]>;
  derived: Record<string, { status: 'operational' | 'degraded' | 'outage' }>;
  incidents: Incident[];
}

async function loadTree(): Promise<Tree> {
  const [rows, derived, incs] = await Promise.all([
    db.select().from(components).where(isNull(components.archivedAt)).orderBy(asc(components.sortOrder)),
    derivedComponentStatuses(),
    getActiveIncidents(),
  ]);
  const byId = new Map<string, Comp>();
  const kids = new Map<string, Comp[]>();
  for (const r of rows) byId.set(r.id, r);
  for (const r of rows) {
    if (!r.parentId) continue;
    const arr = kids.get(r.parentId) ?? [];
    arr.push(r);
    kids.set(r.parentId, arr);
  }
  return { byId, kids, derived: derived as any, incidents: incs.filter((i) => i.status !== 'resolved') };
}

function ownStatus(t: Tree, id: string): ServiceStatus {
  const d = t.derived[id];
  if (d) return d.status;
  const c = t.byId.get(id);
  return c ? mapStatus(c.status) : 'operational';
}
function effective(t: Tree, id: string, seen: Set<string> = new Set()): ServiceStatus {
  if (seen.has(id)) return 'operational'; // cycle guard: don't re-walk a node
  seen.add(id);
  let s = ownStatus(t, id);
  for (const k of t.kids.get(id) ?? []) s = worst(s, effective(t, k.id, seen));
  return s;
}
function subtreeIds(t: Tree, id: string, seen: Set<string> = new Set()): string[] {
  if (seen.has(id)) return []; // cycle guard
  seen.add(id);
  const out = [id];
  for (const k of t.kids.get(id) ?? []) out.push(...subtreeIds(t, k.id, seen));
  return out;
}
function incidentsAt(t: Tree, id: string): Incident[] {
  return t.incidents.filter((i) => (i.affects ?? []).includes(id));
}
function issueCount(t: Tree, id: string): number {
  const ids = new Set(subtreeIds(t, id));
  return t.incidents.filter((i) => (i.affects ?? []).some((a) => ids.has(a))).length;
}
/** root..node chain. */
function chainTo(t: Tree, id: string): Comp[] {
  const out: Comp[] = [];
  const seen = new Set<string>(); // cycle guard: a bad parent edge can't loop forever
  let cur: Comp | undefined = t.byId.get(id);
  while (cur && !seen.has(cur.id)) {
    seen.add(cur.id);
    out.unshift(cur);
    cur = cur.parentId ? t.byId.get(cur.parentId) : undefined;
  }
  return out;
}
/** URL for a node relative to the scope root (root → '/'). */
function hrefFor(t: Tree, id: string, rootId: string): string {
  const chain = chainTo(t, id).map((c) => c.id);
  const ri = chain.indexOf(rootId);
  const segs = ri >= 0 ? chain.slice(ri + 1) : chain;
  return segs.length ? '/' + segs.join('/') : '/';
}

/** Resolve a path (relative to the scope root) → component id, or null. */
function resolvePath(t: Tree, rootId: string, segs: string[]): string | null {
  let cur = rootId;
  for (const seg of segs) {
    const child = (t.kids.get(cur) ?? []).find((c) => c.id === seg);
    if (!child) return null;
    cur = child.id;
  }
  return cur;
}

/** Build the ScopeView for a node addressed by a path under the scope root. */
export async function buildComponentView(scope: string | null, segs: string[]): Promise<ScopeView | null> {
  const t = await loadTree();
  const rootId = scopeRootId(scope);
  if (!t.byId.has(rootId)) return null;
  const id = resolvePath(t, rootId, segs);
  if (!id) return null;

  const node = t.byId.get(id)!;
  const isRoot = id === rootId;
  const status = effective(t, id);
  const kids = t.kids.get(id) ?? [];

  const children: ViewChild[] = kids.map((c) => ({
    id: c.id,
    name: c.name,
    kind: (c.tag ?? c.kind) as ViewChild['kind'],
    status: effective(t, c.id),
    issueCount: issueCount(t, c.id),
    maintCount: 0,
    href: hrefFor(t, c.id, rootId),
  }));

  const chain = chainTo(t, id);
  const ri = chain.findIndex((c) => c.id === rootId);
  const visible = ri >= 0 ? chain.slice(ri) : chain;
  const crumbs: CrumbItem[] = visible.map((c, i) => ({
    label: c.name,
    href: i < visible.length - 1 ? hrefFor(t, c.id, rootId) : undefined,
  }));

  const level: ScopeView['level'] = node.kind === 'organization' ? 'umbrella' : node.kind === 'product' ? 'product' : 'service';

  return {
    status,
    state: statusToState(status),
    isRoot,
    nodeName: node.name,
    level,
    crumbs,
    children,
    attachedIncidents: incidentsAt(t, id),
    issueCount: issueCount(t, id),
    maintCount: 0,
    affectedChildNames: children.filter((c) => c.status !== 'operational').map((c) => c.name),
  };
}

/** Crumbs + affected-path from the scope root to a component (for the incident page). */
export async function componentCrumbs(
  scope: string | null,
  componentId: string,
): Promise<{ crumbs: CrumbItem[]; affectedPath: string[] }> {
  const t = await loadTree();
  const rootId = scopeRootId(scope);
  if (!t.byId.has(componentId) || !t.byId.has(rootId)) return { crumbs: [], affectedPath: [] };
  const chain = chainTo(t, componentId);
  const ri = chain.findIndex((c) => c.id === rootId);
  const visible = ri >= 0 ? chain.slice(ri) : chain;
  return {
    crumbs: visible.map((c) => ({ label: c.name, href: hrefFor(t, c.id, rootId) })),
    affectedPath: visible.map((c) => c.name),
  };
}

/* ── nested subtree for summary.json (client-side drill / badge) ── */

export interface SummaryNode {
  id: string;
  name: string;
  kind: string;
  status: ServiceStatus;
  issueCount: number;
  incidents: { id: string; title: string; severity: string; status: string; auto: boolean; started: string }[];
  children: SummaryNode[];
}

export async function buildSummaryTree(scope: string | null): Promise<SummaryNode | null> {
  const t = await loadTree();
  const rootId = scopeRootId(scope);
  if (!t.byId.has(rootId)) return null;
  const build = (id: string): SummaryNode => {
    const c = t.byId.get(id)!;
    return {
      id,
      name: c.name,
      kind: (c.tag ?? c.kind) as string,
      status: effective(t, id),
      issueCount: issueCount(t, id),
      incidents: incidentsAt(t, id).map((i) => ({
        id: i.id, title: i.title, severity: i.severity, status: i.status, auto: i.auto !== false, started: i.started,
      })),
      children: (t.kids.get(id) ?? []).map((k) => build(k.id)),
    };
  };
  return build(rootId);
}

/* ── write-boundary integrity (single-model enforcement) ─────────── */

/**
 * Does a (non-archived) component with this id exist? This is the SINGLE
 * model: observations, incident affects[], source mappings, and component
 * parents may only reference ids that resolve here. Enforcing it at every
 * write boundary makes orphan references — and the resulting invisible
 * outage — impossible. Archived components are treated as gone.
 */
export async function componentExists(id: string): Promise<boolean> {
  if (!id) return false;
  const rows = await db.select({ archivedAt: components.archivedAt }).from(components)
    .where(eq(components.id, id));
  return rows.length > 0 && rows[0].archivedAt == null;
}

/**
 * Is this a leaf component you may declare on? Leaves are services + hosts and
 * must not be archived. You never declare UP the tree — container (organization
 * / product) status is derived (worst-of-subtree) and bubbles up. Every
 * affects/declare write boundary uses this in addition to componentExists.
 */
export async function isLeafComponent(id: string): Promise<boolean> {
  if (!id) return false;
  const rows = await db.select({ kind: components.kind, archivedAt: components.archivedAt })
    .from(components).where(eq(components.id, id));
  const r = rows[0];
  return !!r && r.archivedAt == null && (r.kind === 'service' || r.kind === 'host');
}

/**
 * Ids of a component and its whole descendant subtree (children, grandchildren,
 * …), reading non-archived rows directly. Used by the archive guard so a
 * cascade-archive can sweep the full branch. Cycle-guarded.
 */
export async function descendantIds(id: string): Promise<string[]> {
  const rows = await db.select({ id: components.id, parentId: components.parentId })
    .from(components).where(isNull(components.archivedAt));
  const kids = new Map<string, string[]>();
  for (const r of rows) {
    if (!r.parentId) continue;
    const arr = kids.get(r.parentId) ?? [];
    arr.push(r.id);
    kids.set(r.parentId, arr);
  }
  const out: string[] = [];
  const seen = new Set<string>();
  const walk = (cur: string) => {
    if (seen.has(cur)) return; // cycle guard
    seen.add(cur);
    out.push(cur);
    for (const k of kids.get(cur) ?? []) walk(k);
  };
  walk(id);
  return out;
}

/**
 * Ids of a component and its whole descendant subtree, reading ALL rows
 * regardless of archived_at. Used by the RESTORE path: a cascade-archive sets
 * archived_at on the whole branch, so descendantIds() (which filters to live
 * rows) can no longer see the archived children to un-archive them. Walking the
 * adjacency over every row lets restore reverse the cascade symmetrically.
 * Cycle-guarded.
 */
export async function descendantIdsIncludingArchived(id: string): Promise<string[]> {
  const rows = await db.select({ id: components.id, parentId: components.parentId })
    .from(components);
  const kids = new Map<string, string[]>();
  for (const r of rows) {
    if (!r.parentId) continue;
    const arr = kids.get(r.parentId) ?? [];
    arr.push(r.id);
    kids.set(r.parentId, arr);
  }
  const out: string[] = [];
  const seen = new Set<string>();
  const walk = (cur: string) => {
    if (seen.has(cur)) return; // cycle guard
    seen.add(cur);
    out.push(cur);
    for (const k of kids.get(cur) ?? []) walk(k);
  };
  walk(id);
  return out;
}

/**
 * Archive-safety guard (C1/C2). Returns the id of the first component in the
 * given set that has a LIVE outage — an open (non-resolved) incident referencing
 * it, OR live observations the quorum engine has DECLARED on it — or null if the
 * whole set is clear. Used before (cascade-)archiving so an archive can never
 * silently hide a declared outage by dropping the node(s) out of the rendered
 * tree.
 */
export async function firstLiveComponent(ids: string[]): Promise<string | null> {
  for (const id of ids) {
    const open = await openIncidentFor(id);
    if (open) return id;
    const ev = await evaluateComponent(id);
    if (ev.state === 'declared') return id;
  }
  return null;
}

/**
 * Walk the component tree from a leaf up to the nearest product (or, failing
 * that, organization) ancestor and return its id. Used to scope incidents /
 * maintenance / notifications to a product. Falls back to UMBRELLA_ID (the
 * seam's umbrella scope) when no product/organization ancestor is found, so
 * nothing is ever silently mis-scoped to a hard-coded product.
 */
export async function productAncestorId(componentId: string): Promise<string> {
  const rows = await db.select({ id: components.id, parentId: components.parentId, kind: components.kind })
    .from(components).where(isNull(components.archivedAt));
  const byId = new Map(rows.map((r) => [r.id, r]));
  let cur = byId.get(componentId);
  let orgFallback: string | null = null;
  while (cur) {
    if (cur.kind === 'product') return cur.id;
    if (cur.kind === 'organization') orgFallback = cur.id;
    cur = cur.parentId ? byId.get(cur.parentId) : undefined;
  }
  return orgFallback ?? UMBRELLA_ID;
}

/**
 * Flat list of leaf components (services + hosts) as the public Service shape,
 * with quorum-derived status overlaid. This is the components-native
 * replacement for the legacy getPublicServices() so status.json AGREES with
 * /api/v1/summary.json (both derive from the same tree + engine). Optional
 * scope filters to a product subtree.
 */
export async function getPublicLeafComponents(scope: string | null): Promise<
  { id: string; name: string; product: string; status: ServiceStatus }[]
> {
  const t = await loadTree();
  const rootId = scopeRootId(scope);
  if (!t.byId.has(rootId)) return [];
  const ids = subtreeIds(t, rootId);
  const out: { id: string; name: string; product: string; status: ServiceStatus }[] = [];
  for (const id of ids) {
    const c = t.byId.get(id)!;
    if (c.kind !== 'service' && c.kind !== 'host') continue;
    // Nearest product ancestor within the loaded tree (root falls back to itself).
    let prod = rootId;
    let cur: Comp | undefined = c;
    while (cur) {
      if (cur.kind === 'product') { prod = cur.id; break; }
      cur = cur.parentId ? t.byId.get(cur.parentId) : undefined;
    }
    out.push({ id: c.id, name: c.name, product: prod, status: effective(t, c.id) });
  }
  return out;
}
