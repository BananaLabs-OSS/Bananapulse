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

export const STATUS_LABEL: Record<ServiceStatus, string> = {
  operational: 'Operational',
  degraded: 'Degraded',
  outage: 'Major outage',
  maintenance: 'Maintenance',
};

/** Severity → color for incident accents. */
export function severityColor(sev: IncidentSeverity): string {
  switch (sev) {
    case 'major': return 'var(--major)';
    case 'moderate': return 'var(--part)';
    case 'minor': return 'var(--degr)';
  }
}

export function severityLabel(sev: IncidentSeverity): string {
  switch (sev) {
    case 'major': return 'Major outage';
    case 'moderate': return 'Partial outage';
    case 'minor': return 'Degraded';
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
