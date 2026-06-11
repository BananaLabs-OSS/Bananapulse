/**
 * Admin single-component endpoints (admin-session auth).
 *   PATCH  /api/v1/admin/components/:id   edit name/kind/parent/tag/brand/domain/sort
 *   DELETE /api/v1/admin/components/:id   ARCHIVE (soft) — never hard-delete
 *
 * Note: status is intentionally NOT editable here. Component status is derived
 * by the engine from observations, never hand-set (status §5 discipline). The
 * id is immutable (it's referenced by observations + incidents.affects).
 */
import type { APIRoute } from 'astro';
import { requireAdmin, ok, err } from '@/lib/admin-api';
import { updateComponentRow, setComponentArchived, ArchiveBlockedError } from '@/lib/db-components';
import { componentExists, descendantIds } from '@/lib/components';
import { COMPONENT_KIND_OPTIONS } from '@/lib/admin/resources';

const KINDS = COMPONENT_KIND_OPTIONS.map((o) => o.value);

export const PATCH: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  const id = ctx.params.id!;
  const b = await ctx.request.json().catch(() => null);
  if (!b) return err('bad_request', 'Invalid JSON body.', 400);
  if (b.action === 'unarchive') { await setComponentArchived(id, false); return ok({ id }); }
  if (b.kind !== undefined && !KINDS.includes(b.kind)) {
    return err('bad_request', `kind must be one of: ${KINDS.join(', ')}.`, 400);
  }
  if (b.parentId !== undefined && b.parentId) {
    if (b.parentId === id) return err('bad_request', 'A component cannot be its own parent.', 400);
    if (!(await componentExists(b.parentId))) return err('bad_request', `Unknown parent component "${b.parentId}".`, 400);
    // Cycle guard (H3): reject a parent that lives in this node's own subtree.
    if ((await descendantIds(id)).includes(b.parentId)) {
      return err('bad_request', 'Cannot set a descendant as the parent (would create a cycle).', 400);
    }
  }
  const u: Record<string, unknown> = {};
  if (b.name) u.name = b.name;
  if (b.kind) u.kind = b.kind;
  if (b.parentId !== undefined) u.parentId = b.parentId || null;
  if (b.tag !== undefined) u.tag = b.tag || null;
  if (b.brand !== undefined) u.brand = b.brand || null;
  if (b.domain !== undefined) u.domain = b.domain || null;
  if (b.sortOrder != null && b.sortOrder !== '') u.sortOrder = Number(b.sortOrder);
  await updateComponentRow(id, u);
  return ok({ id });
};

export const DELETE: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  try {
    await setComponentArchived(ctx.params.id!, true);
  } catch (e) {
    if (e instanceof ArchiveBlockedError) return err('conflict', e.message, 409);
    throw e;
  }
  return ok({ archived: true });
};
