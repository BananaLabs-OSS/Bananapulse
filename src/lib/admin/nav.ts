/**
 * Sidebar nav counts + property scope list for the admin shell.
 * Kept tiny and decoupled so the shell stays product-agnostic.
 */
import { db } from '@/db';
import { incidents, components, sources } from '@/db/schema';
import { ne, isNull, eq, and, count } from 'drizzle-orm';
import { COMPANY } from '@/pulse.config';
import { monitorAdminOwnerConfigured, monitorOwnerProjection } from '@/pages/api/v1/admin/components/pulp-owner';

export async function navCounts(): Promise<Record<string, number>> {
  if (monitorAdminOwnerConfigured()) {
    const projection = await monitorOwnerProjection(true);
    return {
      incidents: projection.incidents.filter((incident) => incident.status !== 'resolved').length,
      components: projection.components.filter(({ component }) => !component.archived).length,
      sources: projection.sources.filter((source) => !source.revoked).length,
    };
  }
  const [inc] = await db.select({ v: count() }).from(incidents).where(ne(incidents.status, 'resolved'));
  const [cmp] = await db.select({ v: count() }).from(components).where(isNull(components.archivedAt));
  const [src] = await db.select({ v: count() }).from(sources).where(isNull(sources.revokedAt));
  return { incidents: inc.v, components: cmp.v, sources: src.v };
}

/** Property scope selector options — product nodes + root. */
export async function propertyOptions(): Promise<{ value: string; label: string }[]> {
  if (monitorAdminOwnerConfigured()) {
    const projection = await monitorOwnerProjection(false);
    return [
      { value: 'all', label: `All â€” ${COMPANY} root` },
      ...projection.components.map(({ component }) => component)
        .filter((c) => !c.archived && c.kind === 'product').map((c) => ({ value: c.id, label: c.name })),
    ];
  }
  const prods = await db.select({ id: components.id, name: components.name }).from(components)
    .where(and(eq(components.kind, 'product'), isNull(components.archivedAt)));
  return [
    { value: 'all', label: `All — ${COMPANY} root` },
    ...prods.map((p) => ({ value: p.id, label: p.name })),
  ];
}

/** Static fallback used where an await is undesirable; resolved list preferred. */
export const PROPERTIES = [{ value: 'all', label: `All — ${COMPANY} root` }];
