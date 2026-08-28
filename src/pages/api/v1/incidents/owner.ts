/**
 * HTTP compatibility adapter for the incident routes.
 *
 * The monitor cell owns incident state.  This file intentionally lives beside
 * the routes, rather than in the general bridge client, because it translates
 * the legacy Bananapulse incident JSON shape (camel-case timestamps) to the
 * monitor wire contract.  It contains no state and never falls back after an
 * owner request has been selected.
 */
import {
  callPulpEvent,
  PULP_EVENTS,
  PulpBridgeError,
  pulpOwnerRequestID,
} from '@/lib/pulp-bridge';

export const MONITOR_VERSION = 'monitor.v1';
const SUBSCRIBER_VERSION = 'bananapulse.subscribers/v1';
const MONITOR_INCIDENT_COMMANDS: Record<string, string> = {
  open: 'open_incident',
  edit: 'edit_incident',
  update: 'update_incident',
  resolve: 'resolve_incident',
  delete: 'delete_incident',
};

export type OwnerIncident = {
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
};

export type OwnerTimeline = {
  id: string;
  incident_id: string;
  at_unix: number;
  label: string;
  body: string;
  author: string;
};

type OwnerProjection = {
  incidents?: OwnerIncident[];
  incident_updates?: OwnerTimeline[];
};

export type OwnerCommand = Record<string, unknown> & {
  version: typeof MONITOR_VERSION;
  id: string;
  kind: string;
  at_unix: number;
};

function iso(unix?: number): string | null {
  return unix && unix > 0 ? new Date(unix * 1_000).toISOString() : null;
}

/** Convert the owner projection to the JSON emitted by the old Drizzle rows. */
export function legacyIncident(incident: OwnerIncident) {
  return {
    id: incident.id,
    title: incident.title,
    summary: incident.summary,
    status: incident.status,
    severity: incident.severity,
    affects: incident.affects,
    auto: incident.auto,
    startedAt: iso(incident.started_at_unix),
    resolvedAt: iso(incident.resolved_at_unix),
    createdAt: iso(incident.created_at_unix ?? incident.started_at_unix),
  };
}

export function legacyTimeline(update: OwnerTimeline) {
  const at = iso(update.at_unix);
  return {
    id: update.id,
    incidentId: update.incident_id,
    at,
    label: update.label,
    body: update.body,
    author: update.author,
    // The legacy schema creates timeline rows at the same instant routes write
    // them. The monitor contract has one durable authored time, so expose it
    // for the legacy createdAt field as well.
    createdAt: at,
  };
}

export async function incidentProjection(incidentId?: string): Promise<OwnerProjection> {
  return callPulpEvent<unknown, OwnerProjection>(PULP_EVENTS.monitorQuery, {
    version: MONITOR_VERSION,
    incident_id: incidentId ?? '',
    at_unix: Math.floor(Date.now() / 1_000),
  });
}

export async function ownerIncident(incidentId: string): Promise<{ incident: OwnerIncident; timeline: OwnerTimeline[] } | null> {
  // Query the projection and filter locally. Asking the cell for an absent id
  // is rightly a domain error, but the privacy-preserving bridge maps owner
  // errors and transport errors to the same HTTP class. This keeps "not found"
  // distinct from an unavailable owner, which must fail closed.
  const projection = await incidentProjection();
  const incident = projection.incidents?.find((candidate) => candidate.id === incidentId);
  if (!incident) return null;
  return {
    incident,
    timeline: (projection.incident_updates ?? []).filter((update) => update.incident_id === incidentId),
  };
}

export function newOwnerCommand(kind: string, identity: string, payload: Record<string, unknown> = {}): OwnerCommand {
  return {
    version: MONITOR_VERSION,
    id: pulpOwnerRequestID(`incident-${kind}`, identity),
    kind: MONITOR_INCIDENT_COMMANDS[kind] ?? kind,
    at_unix: Math.floor(Date.now() / 1_000),
    ...payload,
  };
}

export function sendIncidentCommand(command: OwnerCommand): Promise<unknown> {
  return callPulpEvent<OwnerCommand, unknown>(PULP_EVENTS.monitorAdminCommand, command);
}

/**
 * Commit monitor state first, then create durable subscriber intents through
 * the Lua composition.  The request/event IDs are deterministic so a retry
 * cannot duplicate either the state command or an outbox intent.
 */
export function publishIncidentCommand(command: OwnerCommand, notification: {
  eventId: string;
  subject: string;
  body: string;
}): Promise<unknown> {
  const statusBaseURL = process.env.PUBLIC_STATUS_URL?.trim().replace(/\/$/, '');
  return callPulpEvent(PULP_EVENTS.incidentPublish, {
    monitor_request: command,
    notification_request: {
      version: SUBSCRIBER_VERSION,
      request_id: pulpOwnerRequestID('incident-notification', notification.eventId),
      event_id: notification.eventId,
      subject: notification.subject,
      body: notification.body,
      // The subscriber owner validates this as an absolute URL. Leaving it
      // absent is valid for a local/preview composition; a relative fallback
      // would make an otherwise durable incident command fail after commit.
      ...(statusBaseURL ? { unsubscribe_base_url: `${statusBaseURL}/api/unsubscribe` } : {}),
      occurred_at: new Date(command.at_unix * 1_000).toISOString(),
    },
  });
}

export function ownerFailure(error: unknown): Response {
  // Do not surface bridge or owner internals to the public API. A selected
  // owner path is authoritative: it is unavailable rather than falling back
  // to the legacy database.
  const message = error instanceof PulpBridgeError && error.status === 503
    ? 'Incident owner is unavailable.'
    : 'Incident owner request failed.';
  return new Response(JSON.stringify({ error: { code: 'owner_unavailable', message } }), {
    status: 503,
    headers: { 'Content-Type': 'application/json' },
  });
}
