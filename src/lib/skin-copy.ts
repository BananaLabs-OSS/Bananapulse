/**
 * Status → color / label maps for the skinned public pages.
 * Mirrors the maps in the visual spec (status.html). Colors reference the
 * per-skin CSS custom properties so each skin recolors automatically.
 *
 * We work in the public ServiceStatus vocabulary (operational | degraded |
 * outage | maintenance). The spec's finer-grained "partial" is folded into the
 * existing model: outage → major, degraded → degraded.
 */
import type { ServiceStatus, IncidentSeverity, IncidentStatus } from './types';

export const STATUS_COLOR: Record<ServiceStatus, string> = {
  operational: 'var(--ok)',
  degraded: 'var(--degr)',
  outage: 'var(--major)',
  maintenance: 'var(--maint)',
};

// STATUS = a component's own health (the node's word). UP / DEGRADED / DOWN.
// Distinct from incident SEVERITY (Minor/Moderate/Major) below — the two
// vocabularies are kept separate on purpose; never conflate them.
export const STATUS_LABEL: Record<ServiceStatus, string> = {
  operational: 'Up',
  degraded: 'Degraded',
  outage: 'Down',
  maintenance: 'Maintenance',
};

// Per-LEVEL status vocabulary — the right word depends on WHAT you're describing:
//   SYSTEM  → Operational / Partial / Outage   (the whole thing)
//   PRODUCT → Healthy / Degraded / Unhealthy    (a group of services)
//   SERVICE → Up / Down                         (a single thing — binary)
// "Middle" is an AGGREGATE state (some children down); a leaf service has no
// children, so a degraded service still reads "Up" — its degradation surfaces via
// the incident badge and rolls the parent product up to "Degraded".
export type LevelKind = 'system' | 'product' | 'service';
export function levelOf(levelOrKind: string): LevelKind {
  if (levelOrKind === 'umbrella' || levelOrKind === 'organization') return 'system';
  if (levelOrKind === 'product') return 'product';
  return 'service'; // service | host
}
export function statusWordFor(levelOrKind: string, status: ServiceStatus): string {
  if (status === 'maintenance') return 'Maintenance';
  switch (levelOf(levelOrKind)) {
    case 'system':  return status === 'operational' ? 'Operational' : status === 'degraded' ? 'Partial' : 'Outage';
    case 'product': return status === 'operational' ? 'Healthy' : status === 'degraded' ? 'Degraded' : 'Unhealthy';
    default:        return status === 'outage' ? 'Down' : 'Up'; // service: binary (degraded folds into Up)
  }
}
// Color follows the word: a degraded SERVICE reads "Up", so it shows the ok color.
export function statusColorFor(levelOrKind: string, status: ServiceStatus): string {
  if (levelOf(levelOrKind) === 'service' && status === 'degraded') return STATUS_COLOR.operational;
  return STATUS_COLOR[status];
}

/** Severity → color for incident accents. */
export function severityColor(sev: IncidentSeverity): string {
  switch (sev) {
    case 'major': return 'var(--major)';
    case 'moderate': return 'var(--part)';
    case 'minor': return 'var(--degr)';
  }
}

// SEVERITY = how much an incident matters (its blast radius). Minor/Moderate/Major.
// Separate from STATUS above: a node can read DOWN with a MODERATE incident
// (non-critical) or DOWN with a MAJOR one (critical).
export function severityLabel(sev: IncidentSeverity): string {
  switch (sev) {
    case 'major': return 'Major';
    case 'moderate': return 'Moderate';
    case 'minor': return 'Minor';
  }
}

/** Timeline event color by incident status. */
export function eventColor(status: IncidentStatus): string {
  switch (status) {
    case 'investigating': return 'var(--part)';
    case 'identified': return 'var(--degr)';
    case 'monitoring': return 'var(--maint)';
    case 'resolved': return 'var(--ok)';
  }
}
