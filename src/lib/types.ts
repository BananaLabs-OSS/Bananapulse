/* Status types for the Banana Pulse status page. */

export type ServiceStatus = 'operational' | 'degraded' | 'outage' | 'maintenance';
export type DayStatus = 'ok' | 'deg' | 'out' | 'maint' | 'future';

export type IncidentSeverity = 'minor' | 'moderate' | 'major';
export type IncidentStatus = 'investigating' | 'identified' | 'monitoring' | 'resolved';

export interface Service {
  id: string;
  name: string;
  product: string;
  status: ServiceStatus;
  uptime90d: DayStatus[];
}

export interface TimelineEntry {
  status: IncidentStatus;
  body: string;
  timestamp: string; // ISO 8601
}

export interface Incident {
  id: string;
  title: string;
  severity: IncidentSeverity;
  status: IncidentStatus;
  product: string;
  affects?: string[]; // service IDs affected by this incident
  /** true = engine-opened (detected automatically); false = human-declared. */
  auto?: boolean;
  started: string;   // ISO 8601
  resolved?: string; // ISO 8601
  timeline: TimelineEntry[];
}

export interface Maintenance {
  id: string;
  title: string;
  product: string;
  scheduledStart: string; // ISO 8601
  scheduledEnd: string;   // ISO 8601
  body: string;
}

/** Maps ServiceStatus to the body state class name. */
export function statusToState(s: ServiceStatus): string {
  switch (s) {
    case 'operational': return 'working';
    case 'degraded':    return 'degraded';
    case 'outage':      return 'outage';
    case 'maintenance': return 'queued';
  }
}

/** Returns the worst status from a list of services. */
export function worstStatus(services: Service[]): ServiceStatus {
  const priority: ServiceStatus[] = ['outage', 'degraded', 'maintenance', 'operational'];
  for (const p of priority) {
    if (services.some(s => s.status === p)) return p;
  }
  return 'operational';
}

/** Human-readable label for a service status. */
export function statusLabel(s: ServiceStatus): string {
  switch (s) {
    case 'operational':  return 'OPERATIONAL';
    case 'degraded':     return 'DEGRADED';
    case 'outage':       return 'OUTAGE';
    case 'maintenance':  return 'MAINTENANCE';
  }
}

/** CSS modifier class for a status pill. */
export function statusPillClass(s: ServiceStatus): string {
  switch (s) {
    case 'operational':  return '';
    case 'degraded':     return 'warn';
    case 'outage':       return 'bad';
    case 'maintenance':  return 'maint';
  }
}

/** Human-readable label for an incident status. */
export function incidentStatusLabel(s: IncidentStatus): string {
  switch (s) {
    case 'investigating': return 'INVESTIGATING';
    case 'identified':    return 'IDENTIFIED';
    case 'monitoring':    return 'MONITORING';
    case 'resolved':      return 'RESOLVED';
  }
}

/** Human-readable label for incident severity. */
export function severityLabel(s: IncidentSeverity): string {
  switch (s) {
    case 'minor':    return 'MINOR';
    case 'moderate': return 'MODERATE';
    case 'major':    return 'MAJOR';
  }
}

/** Severity CSS class for badges. */
export function severityClass(s: IncidentSeverity): string {
  switch (s) {
    case 'minor':    return 'sev-minor';
    case 'moderate': return 'sev-moderate';
    case 'major':    return 'sev-major';
  }
}

/** Format an ISO date to a short human-readable string. */
export function formatDate(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
}

/** Format an ISO date to time string. */
export function formatTime(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleTimeString('en-US', { hour: 'numeric', minute: '2-digit' });
}

/** Format an ISO date as relative time (e.g. "2h ago"). */
export function relativeTime(iso: string): string {
  const now = Date.now();
  const then = new Date(iso).getTime();
  const diffMs = now - then;
  const diffMin = Math.floor(diffMs / 60000);
  if (diffMin < 1) return 'just now';
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffH = Math.floor(diffMin / 60);
  if (diffH < 24) return `${diffH}h ago`;
  const diffD = Math.floor(diffH / 24);
  if (diffD < 30) return `${diffD}d ago`;
  return formatDate(iso);
}
