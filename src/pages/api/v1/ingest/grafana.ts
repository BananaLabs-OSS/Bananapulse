/**
 * POST /api/v1/ingest/grafana — Grafana Cloud alerting webhook adapter.
 *
 * THIN ADAPTER over the core engine (mirrors /api/v1/ingest/uptimerobot). Grafana Cloud
 * (and Alertmanager-compatible) alert webhooks POST a fixed envelope with an
 * `alerts[]` array; this route translates each alert into a core observation
 * and funnels it through the SAME quorum engine as /api/v1/ingest via
 * appendObservation(). Vendor vocabulary never reaches the core — it is mapped
 * to a component here / via source_target_map.
 *
 * This is VANTAGE 2 (machine truth, shipped off-box to Grafana Cloud).
 *
 * Auth: a shared secret in GRAFANA_HOOK_SECRET, accepted either as
 *   Authorization: Bearer <secret>      (Grafana's webhook bearer-token field)
 * or
 *   X-Grafana-Hook-Secret: <secret>
 * Both are timing-safe-compared. The observations attribute to a lazily
 * created `Grafana Cloud` adapter source (kind=probe), like the other adapters.
 *
 * Payload shape (Grafana Cloud / Alertmanager grafana_alerts):
 *   {
 *     "alerts": [
 *       {
 *         "status": "firing" | "resolved",
 *         "labels": { "alertname": "...", "instance": "...", "job": "...",
 *                     "severity": "critical|warning|...", "target": "..." },
 *         "annotations": { "summary": "...", "description": "..." }
 *       }, ...
 *     ]
 *   }
 *
 * Signal mapping (per alert):
 *   status=resolved                          -> ok
 *   status=firing & severity in {warning,    -> degraded
 *                  minor,info}
 *   status=firing (anything else, incl.      -> down
 *                  critical/no severity)
 *
 * Target resolution (raw label, in priority order, first present wins):
 *   labels.target -> labels.component -> labels.service -> labels.instance
 *   -> labels.job -> labels.alertname
 * resolved through source_target_map for the grafana source (falls back to the
 * raw label as a component id, matching resolveTarget semantics).
 */
import type { APIRoute } from 'astro';
import { timingSafeEqual } from 'node:crypto';
import { getOrCreateAdapterSource, resolveTarget } from '@/lib/sources';
import { appendObservation, type Signal } from '@/lib/quorum';
import { snapshotComponent, notifyForComponent } from '@/lib/notify';
import { callPulpEvent, PULP_EVENTS, PulpBridgeError, pulpOwnerRequestID, pulpOwnerRouteFamilyConfigured } from '@/lib/pulp-bridge';

const DEGRADED_SEVERITIES = new Set(['warning', 'warn', 'minor', 'info', 'low']);

/**
 * Dead-man TTL for Grafana observations. Grafana re-notifies a still-firing
 * alert every `repeat_interval` (ours: 4h), and each repeat refreshes this
 * horizon via the core's dedup re-assertion path — so the TTL must sit just
 * PAST the repeat interval. 4.5h: a still-firing alert never goes stale, but a
 * Grafana-side blackout (stack gone, webhook broken) expires within one cycle
 * instead of pinning the last reading forever.
 */
const GRAFANA_OBS_TTL_SECONDS = Number(process.env.GRAFANA_HOOK_TTL_SECONDS) || 16200;

/**
 * Pick the observed instant for an alert. Firing → `startsAt`. Resolved →
 * `endsAt` — NOT startsAt: Alertmanager keeps startsAt pinned to the firing
 * instant on resolved notifications, so using it would land the `ok` at the
 * SAME observed time as the `down` and the engine's latest-per-source pick
 * (ordered by observed time) becomes a tie. Alertmanager's zero-value
 * timestamp (0001-01-01) parses as a valid Date, so anything implausibly old
 * is treated as absent. Absent/invalid → undefined (caller uses ingest time).
 */
export function grafanaObservedAt(alert: { status?: unknown; startsAt?: unknown; endsAt?: unknown }): Date | undefined {
  const pickRaw = alert?.status === 'resolved' ? alert?.endsAt : alert?.startsAt;
  if (typeof pickRaw !== 'string') return undefined;
  const d = new Date(pickRaw);
  if (isNaN(d.getTime()) || d.getTime() < Date.UTC(2000, 0, 1)) return undefined;
  return d;
}

function json(body: unknown, status: number) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

type OwnerProjection = { sources: Array<{ id: string; name: string; kind: string; revoked?: boolean; default_ttl_seconds?: number | null }>; mappings: Array<{ source_id: string; raw_label: string; component_id: string }>; components: Array<{ component: { id: string; archived?: boolean } }> };
async function ownerProjection(): Promise<OwnerProjection> { return callPulpEvent(PULP_EVENTS.monitorQuery, { version: 'monitor.v1', include_archived: true, at_unix: Math.floor(Date.now() / 1000) }); }
function ownerFailure(error: unknown) { const status = error instanceof PulpBridgeError && error.status < 500 ? error.status : 503; return json({ error: { code: 'owner_unavailable', message: 'Grafana owner is unavailable.' } }, status); }
async function ownerIngest(sourceId: string, raw: string, signal: Signal, detail: string, observedAt: Date, ttl: number) { const observedAtUnix = Math.floor(observedAt.getTime() / 1000); const eventKey = `${sourceId}\0${raw}\0${signal}\0${observedAtUnix}`; return callPulpEvent('bananapulse.monitor.ingest.authenticated.v1', { version: 'monitor.v1', id: pulpOwnerRequestID('grafana-ingest', eventKey), kind: 'ingest_observation', at_unix: Math.floor(Date.now() / 1000), ingest: { observation_id: pulpOwnerRequestID('observation', eventKey), source_id: sourceId, raw_label: raw, signal, detail, observed_at_unix: observedAtUnix, expires_at_unix: observedAtUnix + ttl } }); }

function secretMatches(provided: string, secret: string): boolean {
  if (!provided || provided.length !== secret.length) return false;
  return timingSafeEqual(Buffer.from(provided), Buffer.from(secret));
}

/** Map one Grafana alert (status + severity label) to a core signal. */
function alertToSignal(status: string, severity: string | undefined): Signal {
  if (status === 'resolved') return 'ok';
  // firing:
  if (severity && DEGRADED_SEVERITIES.has(severity.toLowerCase())) return 'degraded';
  return 'down';
}

/** Pick the raw target label from an alert's labels, in priority order. */
function rawTarget(labels: Record<string, string>): string | null {
  return (
    labels.target ?? labels.component ?? labels.service ??
    labels.instance ?? labels.job ?? labels.alertname ?? null
  );
}

export const POST: APIRoute = async ({ request }) => {
  const secret = process.env.GRAFANA_HOOK_SECRET;
  if (!secret) {
    return json({ error: { code: 'not_configured', message: 'Grafana webhook not configured.' } }, 503);
  }

  // Accept the secret as a Bearer token (Grafana webhook auth) or a header.
  const auth = request.headers.get('authorization') ?? '';
  const bearer = auth.toLowerCase().startsWith('bearer ') ? auth.slice(7).trim() : '';
  const headerSecret = request.headers.get('X-Grafana-Hook-Secret') ?? '';
  // ?key=<secret> query fallback (parity with the uptimerobot adapter) for webhook senders
  // that cannot set a custom header.
  const querySecret = new URL(request.url).searchParams.get('key') ?? '';
  const provided = bearer || headerSecret || querySecret;
  if (!provided || !secretMatches(provided, secret)) {
    return json({ error: { code: 'unauthorized', message: 'Invalid or missing secret.' } }, 401);
  }

  let body: any;
  try {
    body = await request.json();
  } catch {
    return json({ error: { code: 'bad_request', message: 'Body must be JSON.' } }, 400);
  }

  const alerts: any[] = Array.isArray(body?.alerts) ? body.alerts : [];
  if (alerts.length === 0) {
    return json({ error: { code: 'bad_request', message: 'Expected a non-empty `alerts[]` array.' } }, 400);
  }

  const useOwner = pulpOwnerRouteFamilyConfigured('ingest');
  let projection: OwnerProjection | null = null;
  let ownerSource: OwnerProjection['sources'][number] | undefined;
  if (useOwner) {
    try { projection = await ownerProjection(); ownerSource = projection.sources.find((source) => source.name === 'Grafana Cloud' && source.kind === 'probe' && !source.revoked); }
    catch (error) { return ownerFailure(error); }
    if (!ownerSource) return json({ error: { code: 'owner_unavailable', message: 'Grafana owner source is not migrated.' } }, 503);
  }
  const source = useOwner ? null : await getOrCreateAdapterSource('Grafana Cloud', 'probe');

  const results: Array<{ target: string; signal: Signal; mapped: boolean }> = [];
  let accepted = 0;

  for (const alert of alerts) {
    const status = typeof alert?.status === 'string' ? alert.status : 'firing';
    const labels: Record<string, string> = (alert?.labels && typeof alert.labels === 'object') ? alert.labels : {};
    const annotations: Record<string, string> = (alert?.annotations && typeof alert.annotations === 'object') ? alert.annotations : {};

    const raw = rawTarget(labels);
    if (!raw) {
      results.push({ target: '(none)', signal: 'down', mapped: false });
      continue;
    }

    const signal = alertToSignal(status, labels.severity);
    const ownerMapping = projection?.mappings.find((mapping) => mapping.source_id === ownerSource?.id && mapping.raw_label === raw);
    const componentId = useOwner ? ownerMapping?.component_id ?? null : await resolveTarget(source!.id, raw);
    if (!componentId) {
      // No mapping row for this raw label: record as unmapped (skip), so the
      // adapter is loud-but-not-fatal, matching the core's 422 semantics.
      results.push({ target: raw, signal, mapped: false });
      continue;
    }

    const detail = annotations.summary || annotations.description || `grafana:${labels.alertname ?? raw}`;
    // Use the alert's own event time as the observed instant (not our processing
    // time) so a delayed/retried webhook orders correctly and dedups.
    const observedAt = grafanaObservedAt(alert);
    if (useOwner) {
      const component = projection!.components.find((entry) => entry.component.id === componentId)?.component;
      if (!component || component.archived) { results.push({ target: raw, signal, mapped: false }); continue; }
      try { await ownerIngest(ownerSource!.id, raw, signal, detail, observedAt ?? new Date(), ownerSource!.default_ttl_seconds || GRAFANA_OBS_TTL_SECONDS); }
      catch (error) { return ownerFailure(error); }
      accepted++; results.push({ target: raw, signal, mapped: true }); continue;
    }

    // Snapshot before the engine runs so we can narrate the exact transition.
    const before = await snapshotComponent(componentId);
    await appendObservation({
      sourceId: source!.id,
      componentId,
      signal,
      detail,
      observedAt,
      defaultTtlSeconds: source!.defaultTtl ?? GRAFANA_OBS_TTL_SECONDS, // dead-man: stale if Grafana goes silent
    });
    await notifyForComponent(componentId, before);

    accepted++;
    results.push({ target: raw, signal, mapped: true });
  }

  return json({ data: { accepted, total: alerts.length, results } }, 202);
};
