import { createHash, createHmac } from 'node:crypto';

const MONITOR_VERSION = 'monitor.v1';
const SUBSCRIBER_VERSION = 'bananapulse.subscribers/v1';

export const PULP_EVENTS = {
  monitorCommand: 'bananapulse.monitor.command.v1',
  monitorAdminCommand: 'bananapulse.monitor.admin.command.v1',
  monitorMigrationImport: 'bananapulse.monitor.migration.import.v1',
  monitorIngestAuthenticated: 'bananapulse.monitor.ingest.authenticated.v1',
  monitorSweep: 'bananapulse.monitor.sweep.v1',
  monitorQuery: 'bananapulse.monitor.query.v1',
  monitorProjection: 'bananapulse.monitor.projection.v1',
  subscriberSubscribe: 'bananapulse.subscriber.subscribe.v1',
  subscriberConfirm: 'bananapulse.subscriber.confirm.v1',
  subscriberUnsubscribe: 'bananapulse.subscriber.unsubscribe.v1',
  subscriberProjection: 'bananapulse.subscriber.projection.v1',
  subscriberConfirmationResend: 'bananapulse.subscriber.confirmation.resend.v1',
  subscriberAdminList: 'bananapulse.subscriber.admin.list.v1',
  subscriberAdminGet: 'bananapulse.subscriber.admin.get.v1',
  subscriberAdminDelete: 'bananapulse.subscriber.admin.delete.v1',
  subscriberAdminStateSet: 'bananapulse.subscriber.admin.state.set.v1',
  subscriberMigrationImport: 'bananapulse.subscriber.migration.import.v1',
  incidentPublish: 'bananapulse.incident.publish.v1',
  maintenancePublish: 'bananapulse.maintenance.publish.v1',
  emailOutboxClaim: 'bananapulse.host.email.outbox.claim.v1',
  emailReceiptApply: 'bananapulse.host.email.outbox.receipt.apply.v1',
} as const;

export type MonitorStatus = 'operational' | 'degraded' | 'outage';
export type MonitorSignal = 'ok' | 'degraded' | 'down';

export interface MonitorComponent {
  id: string;
  parent_id?: string;
  name: string;
  kind: string;
  tag?: string;
  brand?: string;
  domain?: string;
  uptime_90d?: Array<{ date: string; status: string } | string>;
  sort_order?: number;
  fallback_status: MonitorStatus;
  critical: boolean;
  launched?: boolean;
  created_at_unix?: number;
  archived: boolean;
  archived_at_unix?: number;
  archive_batch_id?: string;
}

export interface MonitorEvaluation {
  component_id: string;
  status: MonitorStatus;
  state: string;
  level?: string;
  reads: Array<{
    source_id: string;
    source_name: string;
    weight: number;
    trusted: boolean;
    kind: string;
    signal: MonitorSignal;
    observed_at_unix: number;
    expires_at_unix?: number;
    stale: boolean;
  }>;
  non_ok_count: number;
  non_ok_weight: number;
  trusted_non_ok_count: number;
  stale_count: number;
  reduced_coverage: boolean;
  has_live_reads: boolean;
}

export interface MonitorProjection {
  version: typeof MONITOR_VERSION;
  revision: number;
  components: Array<{
    component: MonitorComponent;
    own_evaluation: MonitorEvaluation;
    evaluation: MonitorEvaluation;
  }>;
  sources: Array<{
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
  }>;
  mappings: Array<{
    id: string;
    source_id: string;
    raw_label: string;
    component_id: string;
  }>;
  incidents: Array<{
    id: string;
    title: string;
    summary: string;
    status: string;
    severity: string;
    affects: string[];
    auto: boolean;
    started_at_unix: number;
    resolved_at_unix?: number;
    created_at_unix?: number;
  }>;
  incident_updates: Array<{
    id: string;
    incident_id: string;
    at_unix: number;
    label: string;
    body: string;
    author: string;
  }>;
  maintenance: Array<{
    id: string;
    title: string;
    summary: string;
    kind: string;
    scheduled_start_unix: number;
    scheduled_end_unix: number;
    affects: string[];
    created_at_unix?: number;
    cancelled: boolean;
    cancelled_at_unix?: number;
  }>;
}

export interface MonitorCommand {
  version: typeof MONITOR_VERSION;
  id: string;
  kind: string;
  at_unix: number;
  component?: MonitorComponent;
  component_id?: string;
  source?: MonitorProjection['sources'][number];
  source_id?: string;
  mapping?: MonitorProjection['mappings'][number];
  observation?: {
    id: string;
    source_id: string;
    component_id: string;
    signal: MonitorSignal;
    detail?: string;
    observed_at_unix: number;
    expires_at_unix?: number;
  };
  incident?: MonitorProjection['incidents'][number];
  update?: MonitorProjection['incident_updates'][number];
  maintenance?: MonitorProjection['maintenance'][number];
  maintenance_id?: string;
}

export interface MonitorCommandResult {
  version: typeof MONITOR_VERSION;
  command_id: string;
  revision: number;
  deduped: boolean;
}

export interface SubscriberCommandResult {
  version: typeof SUBSCRIBER_VERSION;
  subscriber_id?: string;
  created?: boolean;
  confirmed?: boolean;
  unsubscribed?: boolean;
  intent_count?: number;
}

export interface SubscriberAdminRow {
  id: string;
  email: string;
  state: 'pending' | 'confirmed' | 'unsubscribed';
  confirmedAt: string | null;
  createdAt: string;
}

export class PulpBridgeError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly detail = '',
  ) {
    super(message);
  }
}

export function pulpBridgeConfigured(): boolean {
  return Boolean(process.env.PULP_BRIDGE_URL?.trim());
}

export type PulpOwnerRouteFamily =
  | 'monitor-admin'
  | 'ingest'
  | 'incidents'
  | 'maintenance'
  | 'sweep'
  | 'subscriber-admin'
  | 'auth';

const OWNER_FAMILY_FLAGS: Record<PulpOwnerRouteFamily, string> = {
  'monitor-admin': 'PULP_MONITOR_ADMIN_OWNER_ENABLED',
  ingest: 'PULP_INGEST_OWNER_ENABLED',
  incidents: 'PULP_INCIDENTS_OWNER_ENABLED',
  maintenance: 'PULP_MAINTENANCE_OWNER_ENABLED',
  sweep: 'PULP_SWEEP_OWNER_ENABLED',
  'subscriber-admin': 'PULP_SUBSCRIBERS_ADMIN_OWNER_ENABLED',
  auth: 'PULP_AUTH_OWNER_ENABLED',
};

export function pulpOwnerRouteFamilyConfigured(family: PulpOwnerRouteFamily): boolean {
  return pulpBridgeConfigured() && process.env[OWNER_FAMILY_FLAGS[family]] === 'true';
}

export function pulpMonitorProjectionConfigured(): boolean {
  return pulpBridgeConfigured() && process.env.PULP_MONITOR_OWNER_ENABLED === 'true';
}

export function pulpSubscriberLifecycleConfigured(): boolean {
  return (
    pulpBridgeConfigured() &&
    process.env.PULP_SUBSCRIBERS_OWNER_ENABLED === 'true' &&
    Boolean(process.env.PULP_SUBSCRIBER_TOKEN_SECRET?.trim())
  );
}

function stableID(prefix: string, value: string): string {
  return `${prefix}_${createHash('sha256').update(value).digest('base64url')}`;
}

export function pulpOwnerRequestID(prefix: string, value: string): string {
  return stableID(prefix, value);
}

export function subscriberOwnerIdentity(email: string) {
  const secret = process.env.PULP_SUBSCRIBER_TOKEN_SECRET?.trim();
  if (!secret) throw new PulpBridgeError('Pulp subscriber token secret is not configured.', 503);
  const normalized = email.toLowerCase().trim();
  const token = (purpose: string) =>
    createHmac('sha256', secret).update(`${purpose}\0${normalized}`).digest('base64url');
  return {
    requestId: stableID('subscribe', normalized),
    confirmationToken: token('confirmation'),
    unsubscribeToken: token('unsubscribe'),
  };
}

export function subscriberTokenRequestID(purpose: 'confirm' | 'unsubscribe', token: string): string {
  return stableID(purpose, token);
}

function bridgeURL(event: string): string {
  const base = process.env.PULP_BRIDGE_URL?.trim();
  if (!base) throw new PulpBridgeError('Pulp bridge is not configured.', 503);
  return `${base.replace(/\/$/, '')}/internal/v1/events/${encodeURIComponent(event)}`;
}

export async function callPulpEvent<Request, Response>(
  event: string,
  request: Request,
): Promise<Response> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  const token = process.env.PULP_BRIDGE_TOKEN?.trim();
  if (token) headers['X-Pulp-Bridge-Token'] = token;
  let response: globalThis.Response;
  try {
    response = await fetch(bridgeURL(event), {
      method: 'POST',
      headers,
      body: JSON.stringify(request ?? {}),
      cache: 'no-store',
      signal: AbortSignal.timeout(10_000),
    });
  } catch (error) {
    throw new PulpBridgeError(
      `Pulp bridge request failed: ${error instanceof Error ? error.message : String(error)}`,
      503,
    );
  }
  const text = await response.text();
  if (!response.ok) {
    throw new PulpBridgeError(`Pulp event ${event} was rejected.`, response.status, text.slice(0, 512));
  }
  try {
    return (text ? JSON.parse(text) : {}) as Response;
  } catch {
    throw new PulpBridgeError(`Pulp event ${event} returned invalid JSON.`, 502);
  }
}

export function getMonitorProjection(): Promise<MonitorProjection> {
  return callPulpEvent<Record<string, never>, MonitorProjection>(PULP_EVENTS.monitorProjection, {});
}

export function sendMonitorCommand(command: MonitorCommand): Promise<MonitorCommandResult> {
  return callPulpEvent<MonitorCommand, MonitorCommandResult>(PULP_EVENTS.monitorCommand, command);
}

export function queryMonitor(componentId?: string, atUnix = Math.floor(Date.now() / 1000)): Promise<MonitorProjection> {
  return callPulpEvent(PULP_EVENTS.monitorQuery, {
    version: MONITOR_VERSION,
    component_id: componentId ?? '',
    at_unix: atUnix,
  });
}

export function subscribeWithOwner(request: {
  request_id: string;
  email: string;
  confirmation_token: string;
  unsubscribe_token: string;
  confirmation_subject: string;
  confirmation_body: string;
  requested_at: string;
}): Promise<SubscriberCommandResult> {
  return callPulpEvent(PULP_EVENTS.subscriberSubscribe, {
    version: SUBSCRIBER_VERSION,
    ...request,
  });
}

export function confirmWithOwner(requestId: string, token: string): Promise<SubscriberCommandResult> {
  return callPulpEvent(PULP_EVENTS.subscriberConfirm, {
    version: SUBSCRIBER_VERSION,
    request_id: requestId,
    token,
  });
}

export function unsubscribeWithOwner(requestId: string, token: string): Promise<SubscriberCommandResult> {
  return callPulpEvent(PULP_EVENTS.subscriberUnsubscribe, {
    version: SUBSCRIBER_VERSION,
    request_id: requestId,
    token,
  });
}

export function listSubscribersWithOwner(): Promise<{
  version: string;
  subscribers: SubscriberAdminRow[];
}> {
  return callPulpEvent(PULP_EVENTS.subscriberAdminList, {
    version: SUBSCRIBER_VERSION,
  });
}

export function deleteSubscriberWithOwner(requestId: string, subscriberId: string): Promise<{
  version: string;
  found: boolean;
  changed: boolean;
  state?: string;
}> {
  return callPulpEvent(PULP_EVENTS.subscriberAdminDelete, {
    version: SUBSCRIBER_VERSION,
    request_id: requestId,
    subscriber_id: subscriberId,
  });
}
