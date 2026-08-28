/**
 * Compatibility adapter for the maintenance HTTP resources.
 *
 * The monitor cell owns maintenance state.  These helpers deliberately keep
 * the pre-existing REST shapes (camel-case persisted rows) at the HTTP edge;
 * the monitor contract itself remains snake-case and Unix-second based.
 */
import {
  PULP_EVENTS,
  callPulpEvent,
  getMonitorProjection,
  pulpOwnerRequestID,
} from '@/lib/pulp-bridge';

const MONITOR_VERSION = 'monitor.v1';

export interface OwnerMaintenance {
  id: string;
  title: string;
  summary: string;
  kind: string;
  scheduled_start_unix: number;
  scheduled_end_unix: number;
  affects: string[];
  created_at_unix?: number;
  cancelled?: boolean;
  cancelled_at_unix?: number;
}

export interface LegacyMaintenanceRow {
  id: string;
  title: string;
  summary: string;
  kind: string;
  scheduledStart: string;
  scheduledEnd: string;
  affects: string[];
  createdAt: string;
}

type MonitorProjectionWire = {
  maintenance?: OwnerMaintenance[];
  components?: Array<{
    component: {
      id: string;
      kind: string;
      archived: boolean;
    };
  }>;
};

type MonitorCommand = {
  version: typeof MONITOR_VERSION;
  id: string;
  kind: 'schedule_maintenance' | 'edit_maintenance' | 'delete_maintenance';
  at_unix: number;
  maintenance?: OwnerMaintenance;
  maintenance_patch?: {
    id: string;
    title?: string;
    summary?: string;
    kind?: string;
    scheduled_start_unix?: number;
    scheduled_end_unix?: number;
    affects?: string[];
  };
  maintenance_id?: string;
};

export function toUnixSeconds(value: Date): number {
  return Math.floor(value.getTime() / 1000);
}

export function toLegacyMaintenance(value: OwnerMaintenance): LegacyMaintenanceRow {
  return {
    id: value.id,
    title: value.title,
    summary: value.summary,
    kind: value.kind || 'scheduled',
    scheduledStart: new Date(value.scheduled_start_unix * 1_000).toISOString(),
    scheduledEnd: new Date(value.scheduled_end_unix * 1_000).toISOString(),
    affects: [...value.affects],
    createdAt: new Date((value.created_at_unix || 0) * 1_000).toISOString(),
  };
}

export async function ownerMaintenanceProjection(): Promise<MonitorProjectionWire> {
  // The public bridge type intentionally exposes only the public projection.
  // Maintenance admin needs the durable owner fields added in monitor.v1.
  return getMonitorProjection() as unknown as MonitorProjectionWire;
}

export async function ownerMaintenanceRows(): Promise<LegacyMaintenanceRow[]> {
  const projection = await ownerMaintenanceProjection();
  return (projection.maintenance ?? [])
    .filter((item) => !item.cancelled)
    .sort((a, b) => a.scheduled_start_unix - b.scheduled_start_unix || a.id.localeCompare(b.id))
    .map(toLegacyMaintenance);
}

export async function ownerMaintenanceByID(id: string): Promise<OwnerMaintenance | undefined> {
  const projection = await ownerMaintenanceProjection();
  return projection.maintenance?.find((item) => item.id === id);
}

/** Reproduce the legacy leaf-reference diagnostics from owner state. */
export async function validateOwnerAffects(
  affects: unknown[],
  nonLeafMessage: (id: string) => string,
): Promise<string | null> {
  const projection = await ownerMaintenanceProjection();
  const byID = new Map((projection.components ?? []).map(({ component }) => [component.id, component]));
  for (const id of affects) {
    const component = typeof id === 'string' ? byID.get(id) : undefined;
    if (!component || component.archived) return `Unknown component "${String(id)}".`;
    if (component.kind !== 'service' && component.kind !== 'host') return nonLeafMessage(String(id));
  }
  return null;
}

export function ownerCommandID(prefix: string, value: string): string {
  return pulpOwnerRequestID(prefix, value);
}

export async function sendOwnerMaintenanceCommand(command: MonitorCommand): Promise<void> {
  await callPulpEvent<MonitorCommand, unknown>(PULP_EVENTS.monitorAdminCommand, command);
}

export function ownerNowUnix(): number {
  return Math.floor(Date.now() / 1_000);
}
