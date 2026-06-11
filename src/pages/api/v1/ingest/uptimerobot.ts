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

/**
 * Dead-man TTL for UptimeRobot observations: ~2x a 5-minute probe so a probe that
 * simply dies goes stale instead of pinning the last reading forever.
 */
const UPTIME_OBS_TTL_SECONDS = Number(process.env.UPTIME_HOOK_TTL_SECONDS) || 600;

// Legacy fixed-shape status -> core signal (back-compat).
const LEGACY_SIGNAL_MAP: Record<string, Signal | 'maint'> = {
  ok: 'ok', deg: 'degraded', out: 'down', maint: 'maint',
};

// core signal -> legacy 90-day-bar status string.
const SIGNAL_TO_BAR: Record<Signal, string> = { ok: 'ok', degraded: 'deg', down: 'out' };

function json(body: unknown, status: number) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

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

  const source = await getOrCreateAdapterSource('UptimeRobot', 'probe');

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
    const componentId = await resolveTarget(source.id, raw);
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

    await bump90d(componentId, SIGNAL_TO_BAR[mapped]);
    const before = await snapshotComponent(componentId);
    const { deduped } = await appendObservation({
      sourceId: source.id,
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
      sourceId: source.id,
      componentId: serviceId, // legacy: the raw label IS the component id
      signal: mapped,
      detail: 'uptimerobot',
      defaultTtlSeconds: UPTIME_OBS_TTL_SECONDS,
    });
    await notifyForComponent(serviceId, before);
  }
  return json({ data: { updated: true } }, 200);
};
