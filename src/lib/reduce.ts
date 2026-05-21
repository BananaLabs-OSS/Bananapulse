// reduce.ts — fold a tree of subsystem statuses up to a single rollup.
//
// Severity order: maintenance < operational < degraded < major.
// Maintenance is intentionally below operational so a single planned
// maintenance node doesn't drag an otherwise-green system to "issues".
// A parent's status is max(children) unless explicitly set.

import type { Health, Subsystem } from './canonical.js';

const RANK: Record<Health, number> = {
  maintenance: 0,
  operational: 1,
  degraded: 2,
  major: 3,
};

const BY_RANK: Health[] = ['maintenance', 'operational', 'degraded', 'major'];

export function worse(a: Health, b: Health): Health {
  return RANK[a] >= RANK[b] ? a : b;
}

/** Roll a subsystem subtree into a single status (max of self + descendants). */
export function rollupSubsystem(s: Subsystem): Health {
  let h: Health = s.status;
  if (s.children) {
    for (const c of s.children) h = worse(h, rollupSubsystem(c));
  }
  return h;
}

/** Roll a list of subsystems into an overall status. */
export function rollupOverall(subsystems: Subsystem[]): Health {
  if (subsystems.length === 0) return 'operational';
  let h: Health = 'maintenance';
  for (const s of subsystems) h = worse(h, rollupSubsystem(s));
  // If everything was maintenance-or-better but nothing was actually
  // operational, treat as maintenance (banner-worthy). Otherwise the
  // worst leaf wins.
  return h === 'maintenance' && subsystems.some((s) => rollupSubsystem(s) !== 'maintenance')
    ? 'operational'
    : h;
}

/** Stable sort key so tests / Atom feed are deterministic. */
export function statusRank(h: Health): number {
  return RANK[h];
}

export const HEALTHS_BY_RANK = BY_RANK;
