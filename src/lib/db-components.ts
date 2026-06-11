/**
 * Admin CRUD for the `components` adjacency tree. Status is intentionally NOT
 * settable here — it's derived by the engine (status §5 discipline). Archive,
 * never hard-delete.
 */
import { db } from '@/db';
import { components } from '@/db/schema';
import { eq, isNull, isNotNull, asc, and, inArray } from 'drizzle-orm';
import { descendantIds, descendantIdsIncludingArchived, firstLiveComponent } from './components';

/**
 * Thrown by setComponentArchived when an archive would hide a live outage
 * (C1/C2). Callers surface this as a 409 (API) or a form error (admin page).
 */
export class ArchiveBlockedError extends Error {
  constructor(public readonly componentId: string) {
    super(
      `Cannot archive: component "${componentId}" has a live, declared outage ` +
      `(open incident or active observations). Resolve it first — archiving ` +
      `would hide the outage from every status page.`,
    );
    this.name = 'ArchiveBlockedError';
  }
}

export async function getComponentsAdmin(opts?: { archived?: boolean }) {
  const conds = [];
  if (opts?.archived === false) conds.push(isNull(components.archivedAt));
  if (opts?.archived === true) conds.push(isNotNull(components.archivedAt));
  return db.select().from(components)
    .where(conds.length ? and(...conds) : undefined)
    .orderBy(asc(components.sortOrder));
}

export async function getComponentRow(id: string) {
  const rows = await db.select().from(components).where(eq(components.id, id));
  return rows[0] ?? null;
}

export interface ComponentInput {
  id: string;
  name: string;
  kind: string;
  parentId: string | null;
  tag: string | null;
  brand: string | null;
  domain: string | null;
  sortOrder: number;
}

export async function createComponentRow(v: ComponentInput) {
  await db.insert(components).values({
    id: v.id, name: v.name, kind: v.kind, parentId: v.parentId,
    tag: v.tag, brand: v.brand, domain: v.domain, sortOrder: v.sortOrder, status: 'ok',
  });
}

export async function updateComponentRow(id: string, patch: Record<string, unknown>) {
  if (Object.keys(patch).length) await db.update(components).set(patch).where(eq(components.id, id));
}

/**
 * Archive (soft-delete) or restore a component.
 *
 * On archive we CASCADE the whole descendant subtree (C2) so a live child can
 * never be orphaned under a missing parent, and we first GUARD the entire set
 * (C1/C2): if any node in the subtree has an open incident or live (declared)
 * observations, we refuse with ArchiveBlockedError rather than silently paint a
 * live outage green by dropping the node(s) out of the rendered tree. Restore
 * CASCADES symmetrically: un-archiving a parent un-archives its whole archived
 * subtree, so a node restored after a cascade-archive can never re-appear with
 * its children left invisible (the C2 hidden-services class via the restore
 * path). Restore is scoped to the SAME cascade BATCH: we un-archive only the
 * descendants whose archived_at equals the restored node's own archived_at (a
 * cascade writes one `new Date()` across the whole batch). Descendants with an
 * EARLIER archived_at were retired INDEPENDENTLY before this cascade and must
 * STAY archived — restoring a parent never silently revives a deliberately
 * retired child. We enumerate descendants over ALL rows
 * (descendantIdsIncludingArchived) because descendantIds() filters out archived
 * rows and would not find them.
 */
export async function setComponentArchived(id: string, archived: boolean) {
  if (!archived) {
    const node = await getComponentRow(id);
    if (!node || node.archivedAt == null) return; // already live / missing
    const ids = await descendantIdsIncludingArchived(id);
    // Only reverse rows archived in the same cascade batch (matching timestamp);
    // descendants archived earlier (independently retired) must stay archived.
    await db.update(components).set({ archivedAt: null }).where(
      and(inArray(components.id, ids), eq(components.archivedAt, node.archivedAt)),
    );
    return;
  }
  const ids = await descendantIds(id);
  const live = await firstLiveComponent(ids);
  if (live) throw new ArchiveBlockedError(live);
  await db.update(components).set({ archivedAt: new Date() }).where(inArray(components.id, ids));
}
