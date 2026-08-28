/**
 * UptimeRobot webhook — THIN ADAPTER over the core quorum engine.
 *
 * Accepts UptimeRobot's NATIVE webhook payload. Configure the alert contact's
 * POST value as JSON using their *variables*:
 *   { "monitorID": "*monitorID*", "monitorFriendlyName": "*monitorFriendlyName*",
 *     "alertType": "*alertType*", "alertDateTime": "*alertDateTime*",
 *     "alertDetails": "*alertDetails*" }
 *
 *   alertType 1 = down -> signal 'down'
 *   alertType 2 = up   -> signal 'ok'
 *   alertType 3 = ssl  -> informational (cert-expiry is covered by the dedicated
 *                         blackbox/Grafana cert alerts) — accepted, NOT quorum'd.
 *
 * The raw target (monitorID, else monitorFriendlyName) resolves to a component
 * via source_target_map; resolveTarget falls back to the raw label as a
 * component id, so naming a monitor to equal a component id needs no map row.
 * Attributes to a lazily-created UNTRUSTED 'UptimeRobot' adapter source — an
 * external validator: alone it only WATCHes; a second vantage escalates it
 * (see quorum.ts). There is exactly one core path; this route is one adapter.
 *
 * Back-compat: the original fixed shape { service_id, status } (status in
 * ok|deg|out|maint, service_id IS the component id) is still accepted unchanged.
 *
 * Auth: shared secret in UPTIME_HOOK_SECRET, accepted as the
 *   `X-Uptime-Hook-Secret` header OR a `?key=<secret>` query param (UptimeRobot's
 *   free tier cannot always set a custom header). Both timing-safe-compared.
 *
 * Idempotency + observed time: webhook delivery is at-least-once, so we pass
 * UptimeRobot's `alertDateTime` as the observation's observed instant and let the
 * core dedup identical retries (same source+component+signal+observed instant).
 */
import type { APIRoute } from 'astro';
import { db } from '@/db';
import { components } from '@/db/schema';
import { eq } from 'drizzle-orm';
import { timingSafeEqual } from 'node:crypto';
import { getOrCreateAdapterSource, resolveTarget } from '@/lib/sources';
import { appendObservation, type Signal } from '@/lib/quorum';
import { snapshotComponent, notifyForComponent } from '@/lib/notify';
import { callPulpEvent, PULP_EVENTS, PulpBridgeError, pulpOwnerRequestID, pulpOwnerRouteFamilyConfigured } from '@/lib/pulp-bridge';

/**
 * TTL for UptimeRobot observations. UptimeRobot is a TRANSITION-ONLY webhook: it
 * fires ONCE on up->down and stays silent until down->up. So a "down" must stay
 * LIVE for the whole sustained outage (the next 'up' webhook clears it) — it is
 * NOT a periodic probe and must not be aged out on a periodic dead-man.
 *
 * The old 600s (~2x a 5-min probe) was the bug: in a sustained outage the lone
 * "down" expired after 10 min with nothing to refresh it, dropping the quorum
 * from >=2 live monitors to 1 -> the engine fell from `major` to `watch`
 * (degraded). A critical component that was really DOWN decayed to "degraded".
 *
 * 24h is a sticky window that covers any realistic outage while still acting as a
 * far-out dead-man backstop if UptimeRobot itself dies mid-outage and never sends
 * the 'up'. Other live vantages (Status Prober, Grafana) cover that gap meanwhile.
 */
const UPTIME_OBS_TTL_SECONDS = Number(process.env.UPTIME_HOOK_TTL_SECONDS) || 86400;

// Legacy fixed-shape status -> core signal (back-compat).
const LEGACY_SIGNAL_MAP: Record<string, Signal | 'maint'> = {
  ok: 'ok', deg: 'degraded', out: 'down', maint: 'maint',
};

// core signal -> legacy 90-day-bar status string.
const SIGNAL_TO_BAR: Record<Signal, string> = { ok: 'ok', degraded: 'deg', down: 'out' };

function json(body: unknown, status: number) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

type OwnerProjection = { sources: Array<{ id: string; name: string; kind: string; revoked?: boolean }>; mappings: Array<{ source_id: string; raw_label: string; component_id: string }>; components: Array<{ component: { id: string; archived?: boolean; uptime_90d?: unknown[] } }> };
function ownerFailure(error: unknown) { const status = error instanceof PulpBridgeError && error.status < 500 ? error.status : 503; return json({ error: { code: 'owner_unavailable', message: 'UptimeRobot owner is unavailable.' } }, status); }
async function ownerProjection(): Promise<OwnerProjection> { return callPulpEvent(PULP_EVENTS.monitorQuery, { version: 'monitor.v1', include_archived: true, at_unix: Math.floor(Date.now() / 1000) }); }
function ownerBar(current: unknown[] | undefined, status: string) { const today = new Date().toISOString().split('T')[0]; const values = Array.isArray(current) ? [...current] : []; const i = values.findIndex((v: any) => v?.date === today); const next = { date: today, status }; if (i >= 0) values[i] = next; else values.push(next); return values.slice(-90); }
async function ownerIngest(sourceId: string, raw: string, signal: Signal, detail: string, observedAt: Date | null, expiresIn: number, componentId: string, uptime: unknown[]) { const now = observedAt ?? new Date(); const observedAtUnix = Math.floor(now.getTime() / 1000); const eventKey = `${sourceId}\0${raw}\0${signal}\0${observedAtUnix}`; return callPulpEvent('bananapulse.monitor.ingest.authenticated.v1', { version: 'monitor.v1', id: pulpOwnerRequestID('uptime-ingest', eventKey), kind: 'ingest_observation', at_unix: Math.floor(Date.now() / 1000), component_patch: { id: componentId, uptime_90d: uptime }, ingest: { observation_id: pulpOwnerRequestID('observation', eventKey), source_id: sourceId, raw_label: raw, signal, detail, observed_at_unix: observedAtUnix, expires_at_unix: observedAtUnix + expiresIn } }); }

function secretMatches(provided: string, secret: string): boolean {
  if (!provided || provided.length !== secret.length) return false;
  return timingSafeEqual(Buffer.from(provided), Buffer.from(secret));
}

/** UptimeRobot alertType -> core signal, or 'ssl' (informational) / null (unknown). */
export function uptimeAlertTypeToSignal(alertType: unknown): Signal | 'ssl' | null {
  switch (String(alertType)) {
    case '1': return 'down';
    case '2': return 'ok';
    case '3': return 'ssl';
    default: return null;
  }
}

/** UptimeRobot alertDateTime (unix seconds) -> Date, or null if absent/invalid. */
export function parseUptimeObservedAt(alertDateTime: unknown): Date | null {
  if (alertDateTime == null || alertDateTime === '') return null;
  const secs = Number(alertDateTime);
  if (!Number.isFinite(secs) || secs <= 0) return null;
  return new Date(secs * 1000);
}

/** Raw target label from an UptimeRobot payload: monitorID, else friendly name. */
export function uptimeRawTarget(body: any): string | null {
  const id = body?.monitorID != null ? String(body.monitorID).trim() : '';
  if (id) return id;
  const name = typeof body?.monitorFriendlyName === 'string' ? body.monitorFriendlyName.trim() : '';
  return name || null;
}

/** Maintain the public 90-day uptime bar for a component (legacy display). */
async function bump90d(componentId: string, barStatus: string): Promise<boolean> {
  const rows = await db.select().from(components).where(eq(components.id, componentId));
  const svc = rows[0];
  if (!svc || svc.archivedAt != null) return false;
  const today = new Date().toISOString().split('T')[0];
  const uptime = Array.isArray(svc.uptime90d) ? [...(svc.uptime90d as any[])] : [];
  const i = uptime.findIndex((d: any) => d.date === today);
  if (i >= 0) uptime[i] = { date: today, status: barStatus };
  else uptime.push({ date: today, status: barStatus });
  await db.update(components).set({ uptime90d: uptime.slice(-90) }).where(eq(components.id, componentId));
  return true;
}

export const POST: APIRoute = async ({ request }) => {
  const secret = process.env.UPTIME_HOOK_SECRET;
  if (!secret) return json({ error: { code: 'not_configured', message: 'Webhook not configured.' } }, 503);

  const headerSecret = request.headers.get('X-Uptime-Hook-Secret') ?? '';
  const querySecret = new URL(request.url).searchParams.get('key') ?? '';
  if (!secretMatches(headerSecret || querySecret, secret)) {
    return json({ error: { code: 'unauthorized', message: 'Invalid or missing secret.' } }, 401);
  }

  let body: any;
  try { body = await request.json(); }
  catch { return json({ error: { code: 'bad_request', message: 'Body must be JSON.' } }, 400); }

  const useOwner = pulpOwnerRouteFamilyConfigured('ingest');
  let owner: OwnerProjection | null = null;
  let ownerSource: OwnerProjection['sources'][number] | undefined;
  if (useOwner) {
    try { owner = await ownerProjection(); ownerSource = owner.sources.find((source) => source.name === 'UptimeRobot' && source.kind === 'probe' && !source.revoked); }
    catch (error) { return ownerFailure(error); }
    if (!ownerSource) return json({ error: { code: 'owner_unavailable', message: 'UptimeRobot owner source is not migrated.' } }, 503);
  }
  const source = useOwner ? null : await getOrCreateAdapterSource('UptimeRobot', 'probe');

  // ── NATIVE UptimeRobot payload (has alertType) ───────────────────────────
  if (body?.alertType !== undefined) {
    const mapped = uptimeAlertTypeToSignal(body.alertType);
    if (mapped === null) {
      return json({ error: { code: 'bad_request', message: 'Unknown alertType (expected 1=down, 2=up, 3=ssl).' } }, 400);
    }
    const raw = uptimeRawTarget(body);
    if (!raw) {
      return json({ error: { code: 'bad_request', message: 'monitorID or monitorFriendlyName is required.' } }, 400);
    }
    // alertType 3 (SSL expiry) is informational — cert-expiry is covered by the
    // dedicated blackbox/Grafana cert alerts; do not drive an up/down incident.
    if (mapped === 'ssl') {
      return json({ data: { accepted: false, reason: 'ssl-notification-informational', target: raw } }, 202);
    }
    const ownerMapping = owner?.mappings.find((mapping) => mapping.source_id === ownerSource?.id && mapping.raw_label === raw);
    const componentId = useOwner ? ownerMapping?.component_id ?? null : await resolveTarget(source!.id, raw);
    if (!componentId) {
      return json({
        error: {
          code: 'unmapped_target',
          message: `No mapping for UptimeRobot target "${raw}". Add a source_target_map row, or name the monitor to match a component id.`,
        },
      }, 422);
    }
    const observedAt = parseUptimeObservedAt(body.alertDateTime) ?? undefined;
    const detail = (typeof body.alertDetails === 'string' && body.alertDetails) || `uptime:${body.monitorFriendlyName ?? raw}`;

    if (useOwner) {
      const component = owner!.components.find((entry) => entry.component.id === componentId)?.component;
      if (!component || component.archived) return json({ error: { code: 'unmapped_target', message: `No mapping for UptimeRobot target "${raw}". Add a source_target_map row, or name the monitor to match a component id.` } }, 422);
      try { const result: any = await ownerIngest(ownerSource!.id, raw, mapped, (typeof body.alertDetails === 'string' && body.alertDetails) || `uptime:${body.monitorFriendlyName ?? raw}`, parseUptimeObservedAt(body.alertDateTime), UPTIME_OBS_TTL_SECONDS, componentId, ownerBar(component.uptime_90d, SIGNAL_TO_BAR[mapped])); return json({ data: { accepted: true, component_id: componentId, signal: mapped, deduped: !!result.deduped } }, 202); }
      catch (error) { return ownerFailure(error); }
    }
    await bump90d(componentId, SIGNAL_TO_BAR[mapped]);
    const before = await snapshotComponent(componentId);
    const { deduped } = await appendObservation({
      sourceId: source!.id,
      componentId,
      signal: mapped,
      detail,
      observedAt,
      defaultTtlSeconds: UPTIME_OBS_TTL_SECONDS, // dead-man: stale if the probe dies
    });
    await notifyForComponent(componentId, before);
    return json({ data: { accepted: true, component_id: componentId, signal: mapped, deduped } }, 202);
  }

  // ── LEGACY fixed shape { service_id, status } (back-compat) ───────────────
  const serviceId = body?.service_id;
  const status = body?.status;
  if (!serviceId || !status) {
    return json({ error: { code: 'bad_request', message: 'Expected an UptimeRobot payload (alertType) or legacy { service_id, status }.' } }, 400);
  }
  const mapped = LEGACY_SIGNAL_MAP[status];
  if (!mapped) {
    return json({ error: { code: 'bad_request', message: `status must be one of: ${Object.keys(LEGACY_SIGNAL_MAP).join(', ')}` } }, 400);
  }
  if (useOwner) {
    const mapping = owner!.mappings.find((entry) => entry.source_id === ownerSource!.id && entry.raw_label === serviceId);
    const component = mapping && owner!.components.find((entry) => entry.component.id === mapping.component_id)?.component;
    if (!mapping || !component || component.archived) return json({ error: { code: 'not_found', message: 'Component not found.' } }, 404);
    const uptime = ownerBar(component.uptime_90d, status === 'maint' ? 'maint' : SIGNAL_TO_BAR[mapped as Signal]);
    try {
      if (mapped === 'maint') {
        if (!pulpOwnerRouteFamilyConfigured('monitor-admin')) return json({ error: { code: 'owner_unavailable', message: 'UptimeRobot maintenance owner is unavailable.' } }, 503);
        await callPulpEvent('bananapulse.monitor.admin.command.v1', { version: 'monitor.v1', id: pulpOwnerRequestID('uptime-maint', `${mapping.component_id}\0${JSON.stringify(uptime)}`), kind: 'edit_component', at_unix: Math.floor(Date.now() / 1000), component_patch: { id: mapping.component_id, uptime_90d: uptime } });
      } else await ownerIngest(ownerSource!.id, serviceId, mapped, 'uptimerobot', null, UPTIME_OBS_TTL_SECONDS, mapping.component_id, uptime);
      return json({ data: { updated: true } }, 200);
    } catch (error) { return ownerFailure(error); }
  }
  const rows = await db.select().from(components).where(eq(components.id, serviceId));
  const svc = rows[0];
  if (!svc || svc.archivedAt != null) {
    return json({ error: { code: 'not_found', message: 'Component not found.' } }, 404);
  }
  await bump90d(serviceId, status === 'maint' ? 'maint' : SIGNAL_TO_BAR[mapped as Signal]);
  // `maint` is not an observation signal (maintenance is a separate, scheduled
  // concern) — recorded only on the 90-day bar, not the quorum engine.
  if (mapped !== 'maint') {
    const before = await snapshotComponent(serviceId);
    await appendObservation({
      sourceId: source!.id,
      componentId: serviceId, // legacy: the raw label IS the component id
      signal: mapped,
      detail: 'uptimerobot',
      defaultTtlSeconds: UPTIME_OBS_TTL_SECONDS,
    });
    await notifyForComponent(serviceId, before);
  }
  return json({ data: { updated: true } }, 200);
};
