/**
 * HTTP compatibility adapter for the monitor-admin owner.
 *
 * These types deliberately mirror the bridge's JSON ABI (snake_case).  Route
 * handlers translate only at the HTTP edge, so the monitor cell remains the
 * authoritative state owner while the old admin response shapes stay stable.
 */
import {
  PulpBridgeError,
  callPulpEvent,
  pulpOwnerRequestID,
  pulpOwnerRouteFamilyConfigured,
} from '@/lib/pulp-bridge';

export const MONITOR_VERSION = 'monitor.v1';
export const MONITOR_ADMIN_EVENT = 'bananapulse.monitor.admin.command.v1';
export const MONITOR_QUERY_EVENT = 'bananapulse.monitor.query.v1';
export const AUTH_SOURCE_IMPORT_EVENT = 'bananapulse.host.auth.source-credential.admin.import.v1';
export const AUTH_SOURCE_ROTATE_EVENT = 'bananapulse.host.auth.source-credential.admin.rotate.v1';
export const AUTH_SOURCE_REVOKE_EVENT = 'bananapulse.host.auth.source-credential.admin.revoke.v1';
export const HOST_SOURCE_ADMIN_CREATE_EVENT = 'bananapulse.host.source.admin.create.v1';
export const HOST_SOURCE_ADMIN_ROTATE_EVENT = 'bananapulse.host.source.admin.rotate.v1';
export const HOST_SOURCE_ADMIN_REVOKE_EVENT = 'bananapulse.host.source.admin.revoke.v1';
export const HOST_SOURCE_VERSION = 'bananapulse.host.source/v1';

export interface OwnerComponent {
  id: string;
  parent_id?: string;
  name: string;
  kind: string;
  tag?: string;
  brand?: string;
  domain?: string;
  uptime_90d?: Array<{ date: string; status: string }>;
  sort_order?: number;
  fallback_status?: 'operational' | 'degraded' | 'outage';
  critical?: boolean;
  launched?: boolean;
  created_at_unix?: number;
  archived?: boolean;
  archived_at_unix?: number;
}

export interface OwnerSource {
  id: string;
  name: string;
  weight: number;
  kind: string;
  trusted: boolean;
  direct_targets?: boolean;
  default_ttl_seconds: number | null;
  created_at_unix?: number;
  revoked: boolean;
  revoked_at_unix?: number;
}

export interface OwnerMapping {
  id: string;
  source_id: string;
  raw_label: string;
  component_id: string;
}

export interface OwnerProjection {
  version: string;
  revision: number;
  components: Array<{ component: OwnerComponent }>;
  sources: OwnerSource[];
  mappings: OwnerMapping[];
  incidents: Array<{ id: string; status: string }>;
}

export interface OwnerCommandResult {
  version: string;
  command_id: string;
  revision: number;
  deduped: boolean;
  component_ids?: string[];
  mapping_id?: string;
}

export function monitorAdminOwnerConfigured(): boolean {
  return pulpOwnerRouteFamilyConfigured('monitor-admin');
}

/** Source writes create/rotate a credential as well as monitor state. */
export function sourceOwnerConfigured(): boolean {
  return monitorAdminOwnerConfigured() && pulpOwnerRouteFamilyConfigured('auth');
}

export function ownerRequestID(operation: string, request: Request, subject = ''): string {
  // A caller can make a retried mutation durable by supplying Idempotency-Key.
  // Existing clients have no such header, so each independently submitted form
  // gets a distinct command instead of accidentally deduping a later edit.
  const key = request.headers.get('Idempotency-Key')?.trim() || crypto.randomUUID();
  return pulpOwnerRequestID(`bananapulse-${operation}`, `${subject}\0${key}`);
}

export async function monitorOwnerProjection(includeArchived = true): Promise<OwnerProjection> {
  return callPulpEvent(MONITOR_QUERY_EVENT, {
    version: MONITOR_VERSION,
    include_archived: includeArchived,
    at_unix: Math.floor(Date.now() / 1000),
  });
}

export async function monitorAdminCommand(command: Record<string, unknown>): Promise<OwnerCommandResult> {
  return callPulpEvent(MONITOR_ADMIN_EVENT, {
    version: MONITOR_VERSION,
    at_unix: Math.floor(Date.now() / 1000),
    ...command,
  });
}

export async function importSourceCredential(request: Record<string, unknown>) {
  return callPulpEvent(AUTH_SOURCE_IMPORT_EVENT, {
    version: 'bananapulse.auth/v1',
    ...request,
  });
}

export async function rotateSourceCredential(request: Record<string, unknown>) {
  return callPulpEvent(HOST_SOURCE_ADMIN_ROTATE_EVENT, {
    version: 'bananapulse.auth/v1',
    ...request,
  });
}

export async function revokeSourceCredential(request: Record<string, unknown>) {
  return callPulpEvent(AUTH_SOURCE_REVOKE_EVENT, {
    version: 'bananapulse.auth/v1',
    ...request,
  });
}

export async function createSourceWithOwnerSaga(request: Record<string, unknown>) {
  return callPulpEvent(HOST_SOURCE_ADMIN_CREATE_EVENT, {
    version: HOST_SOURCE_VERSION,
    ...request,
  });
}

export async function revokeSourceWithOwnerSaga(request: Record<string, unknown>) {
  return callPulpEvent(HOST_SOURCE_ADMIN_REVOKE_EVENT, {
    version: HOST_SOURCE_VERSION,
    ...request,
  });
}

function isoAt(unix?: number): string | null {
  return unix && unix > 0 ? new Date(unix * 1000).toISOString() : null;
}

function legacyStatus(status?: string): string {
  return status === 'operational' ? 'ok' : status ?? 'ok';
}

export function componentAdminRow(component: OwnerComponent) {
  return {
    id: component.id,
    parentId: component.parent_id || null,
    name: component.name,
    kind: component.kind,
    tag: component.tag || null,
    status: legacyStatus(component.fallback_status),
    uptime90d: component.uptime_90d ?? [],
    sortOrder: component.sort_order ?? 0,
    brand: component.brand || null,
    domain: component.domain || null,
    launched: component.launched ?? true,
    createdAt: isoAt(component.created_at_unix) ?? new Date(0).toISOString(),
    archivedAt: isoAt(component.archived_at_unix),
  };
}

export function sourceAdminRow(source: OwnerSource) {
  return {
    id: source.id,
    name: source.name,
    weight: source.weight,
    kind: source.kind,
    trusted: source.trusted,
    defaultTtl: source.default_ttl_seconds,
    createdAt: isoAt(source.created_at_unix) ?? new Date(0).toISOString(),
    revokedAt: isoAt(source.revoked_at_unix),
  };
}

export function mappingAdminRow(mapping: OwnerMapping) {
  return {
    id: mapping.id,
    sourceId: mapping.source_id,
    rawLabel: mapping.raw_label,
    componentId: mapping.component_id,
  };
}

export function ownerError(error: unknown): { status: number; message: string } {
  if (!(error instanceof PulpBridgeError)) {
    return { status: 502, message: 'Pulp owner request failed.' };
  }
  let message = error.message;
  try {
    const detail = JSON.parse(error.detail) as { error?: string | { message?: string } };
    if (typeof detail.error === 'string') message = detail.error;
    else if (detail.error?.message) message = detail.error.message;
  } catch {
    // The bridge still supplies a stable HTTP status if an intermediary used
    // a non-JSON response.
  }
  return { status: error.status, message };
}
