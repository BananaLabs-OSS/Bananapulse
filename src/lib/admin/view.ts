/**
 * Small view helpers shared across the generic admin screens. Kept separate
 * from the public-page helpers so the admin's blueprint-green palette and
 * vocabulary live in one place.
 */
import type { FieldOption } from './resources';
import { getComponentsAdmin } from '@/lib/db-components';
import { db } from '@/db';
import { observations, sourceTargetMap } from '@/db/schema';
import { sql } from 'drizzle-orm';
import { evaluateComponent, type ComponentEvaluation } from '@/lib/quorum';
import { monitorAdminOwnerConfigured, monitorOwnerProjection } from '@/pages/api/v1/admin/components/pulp-owner';

const PILL_COLOR: Record<string, string> = {
  operational: 'var(--ok)', ok: 'var(--ok)', resolved: 'var(--ok)',
  maintenance: 'var(--maint)', scheduled: 'var(--maint)',
  degraded: 'var(--degr)', moderate: 'var(--degr)',
  minor: 'var(--degr)', partial: 'var(--part)',
  major: 'var(--major)', outage: 'var(--major)', down: 'var(--major)',
  investigating: 'var(--part)', identified: 'var(--degr)', monitoring: 'var(--maint)',
  watch: 'var(--degr)', declared: 'var(--major)', stale: 'var(--faint)',
};

/** HTML-escape a free-text value before it is emitted via set:html. */
function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

/** Inline pill HTML for a status/severity/state token. */
export function pillHtml(key: string, label?: string): string {
  const c = PILL_COLOR[key] ?? 'var(--muted)';
  const text = (label ?? key).toUpperCase();
  return `<span class="pill" style="color:${c};border:1px solid ${c};background:rgba(255,255,255,.04)">${escapeHtml(text)}</span>`;
}

export function fmtDate(d: Date | string | null | undefined): string {
  if (!d) return '—';
  const date = d instanceof Date ? d : new Date(d);
  if (isNaN(date.getTime())) return '—';
  return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
}

export function fmtDateTime(d: Date | string | null | undefined): string {
  if (!d) return '—';
  const date = d instanceof Date ? d : new Date(d);
  if (isNaN(date.getTime())) return '—';
  return date.toLocaleString('en-US', { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' });
}

/**
 * Component options for incident/maintenance "affects" pickers. ONLY the things
 * that can actually break — services + hosts (the leaves). Containers that merely
 * hold breakable things (organization, products) are excluded: you never declare
 * UP the tree; their status is derived (worst-of-subtree) and bubbles up.
 */
export async function componentOptions(): Promise<FieldOption[]> {
  if (monitorAdminOwnerConfigured()) {
    const projection = await monitorOwnerProjection(false);
    return projection.components.map(({ component }) => component)
      .filter((c) => !c.archived && (c.kind === 'service' || c.kind === 'host'))
      .map((c) => ({ value: c.id, label: `${c.name} Â· ${c.kind}` }));
  }
  const comps = await getComponentsAdmin({ archived: false });
  return comps
    .filter((c) => c.kind === 'service' || c.kind === 'host')
    .map((c) => ({ value: c.id, label: `${c.name} · ${c.kind}` }));
}

/** Parent options for the component tree — any node, plus a top-level option. */
export async function componentParentOptions(): Promise<FieldOption[]> {
  if (monitorAdminOwnerConfigured()) {
    const projection = await monitorOwnerProjection(false);
    return [
      { value: '', label: '(none â€” top level / organization root)' },
      ...projection.components.map(({ component: c }) => ({ value: c.id, label: `${c.name} Â· ${c.kind}` })),
    ];
  }
  const comps = await getComponentsAdmin({ archived: false });
  return [
    { value: '', label: '(none — top level / organization root)' },
    ...comps.map((c) => ({ value: c.id, label: `${c.name} · ${c.kind}` })),
  ];
}

/** Product-node options for select fields (kind = product). */
export async function productOptions(): Promise<FieldOption[]> {
  if (monitorAdminOwnerConfigured()) {
    const projection = await monitorOwnerProjection(false);
    return projection.components.map(({ component }) => component)
      .filter((c) => !c.archived && c.kind === 'product').map((c) => ({ value: c.id, label: c.name }));
  }
  const comps = await getComponentsAdmin({ archived: false });
  return comps.filter((c) => c.kind === 'product').map((c) => ({ value: c.id, label: c.name }));
}

/**
 * READ-ONLY situational evaluation of every component the engine knows about.
 * Unlike sweepQuorum this NEVER reconciles (never opens/closes incidents) — the
 * dashboard only observes what the engine sees, it doesn't act.
 */
export async function evaluateAll(now = new Date()): Promise<ComponentEvaluation[]> {
  const rows = await db.execute<{ component_id: string }>(sql`
    SELECT DISTINCT component_id FROM ${observations}
    UNION
    SELECT DISTINCT component_id FROM ${sourceTargetMap}
  `);
  const out: ComponentEvaluation[] = [];
  for (const r of rows) out.push(await evaluateComponent(r.component_id, now));
  return out;
}
