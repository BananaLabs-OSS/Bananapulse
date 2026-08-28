/**
 * Status Prober — THIN ADAPTER over the core quorum engine.
 *
 * The status page's OWN scheduled prober (netlify/functions/status-probe.mjs)
 * runs on Netlify — a failure domain independent of your origin boxes (and any
 * on-box reporters) AND of external monitors. It's the SECOND external vantage, so when a
 * box dies and the on-box eyes go dark, two independent externals (this + UptimeRobot)
 * can agree → the quorum can CONFIRM the outage instead of stalling at one vote.
 *
 * Attributes to a lazily-created UNTRUSTED 'Status Prober' source (an external
 * validator: alone it only WATCHes; a second vantage escalates — see quorum.ts).
 *
 * Body: { probes: [{ component: "<component id>", up: true|false }, ...] }
 *   `component` is a component id directly (the prober knows its own targets).
 *   `up` true → signal 'ok', false → 'down'.
 *
 * Auth: shared secret in UPTIME_HOOK_SECRET (reused — same Netlify env), via the
 *   `X-Status-Probe-Secret` header OR `?key=<secret>`. Timing-safe compared.
 */
import type { APIRoute } from 'astro';
import { db } from '@/db';
import { components } from '@/db/schema';
import { eq } from 'drizzle-orm';
import { timingSafeEqual } from 'node:crypto';
import { getOrCreateAdapterSource } from '@/lib/sources';
import { appendObservation, type Signal } from '@/lib/quorum';
import { snapshotComponent, notifyForComponent } from '@/lib/notify';
import { callPulpEvent, PULP_EVENTS, PulpBridgeError, pulpOwnerRequestID, pulpOwnerRouteFamilyConfigured } from '@/lib/pulp-bridge';

// Dead-man TTL: ~3x the 5-min probe so a prober that simply dies goes stale
// instead of pinning its last reading forever.
const STATUS_PROBE_TTL_SECONDS = Number(process.env.STATUS_PROBE_TTL_SECONDS) || 900;
const SIGNAL_TO_BAR: Record<Signal, string> = { ok: 'ok', degraded: 'deg', down: 'out' };

function json(body: unknown, status: number) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}
function secretMatches(provided: string, secret: string): boolean {
  if (!provided || provided.length !== secret.length) return false;
  return timingSafeEqual(Buffer.from(provided), Buffer.from(secret));
}
type OwnerProjection = { sources: Array<{ id: string; name: string; kind: string; revoked?: boolean; direct_targets?: boolean }>; components: Array<{ component: { id: string; archived?: boolean; uptime_90d?: unknown[] } }> };
async function ownerProjection(): Promise<OwnerProjection> { return callPulpEvent(PULP_EVENTS.monitorQuery, { version: 'monitor.v1', include_archived: true, at_unix: Math.floor(Date.now() / 1000) }); }
function ownerBar(current: unknown[] | undefined, status: string) { const today = new Date().toISOString().split('T')[0]; const values = Array.isArray(current) ? [...current] : []; const i = values.findIndex((v: any) => v?.date === today); const next = { date: today, status }; if (i >= 0) values[i] = next; else values.push(next); return values.slice(-90); }
function ownerFailure(error: unknown) { const status = error instanceof PulpBridgeError && error.status < 500 ? error.status : 503; return json({ error: { code: 'owner_unavailable', message: 'Status Prober owner is unavailable.' } }, status); }
async function ownerIngest(sourceId: string, componentId: string, signal: Signal, uptime: unknown[], runKey: string) { const now = Math.floor(Date.now() / 1000); const eventKey = `${sourceId}\0${componentId}\0${signal}\0${runKey}`; return callPulpEvent('bananapulse.monitor.ingest.authenticated.v1', { version: 'monitor.v1', id: pulpOwnerRequestID('status-probe-ingest', eventKey), kind: 'ingest_observation', at_unix: now, component_patch: { id: componentId, uptime_90d: uptime }, ingest: { observation_id: pulpOwnerRequestID('observation', eventKey), source_id: sourceId, component_id: componentId, signal, detail: 'status-probe', observed_at_unix: now, expires_at_unix: now + STATUS_PROBE_TTL_SECONDS } }); }

/** Maintain the public 90-day uptime bar for a component (mirrors the other adapters). */
async function bump90d(componentId: string, barStatus: string): Promise<void> {
  const rows = await db.select().from(components).where(eq(components.id, componentId));
  const svc = rows[0];
  if (!svc || svc.archivedAt != null) return;
  const today = new Date().toISOString().split('T')[0];
  const uptime = Array.isArray(svc.uptime90d) ? [...(svc.uptime90d as any[])] : [];
  const i = uptime.findIndex((d: any) => d?.date === today);
  if (i >= 0) uptime[i] = { date: today, status: barStatus };
  else uptime.push({ date: today, status: barStatus });
  await db.update(components).set({ uptime90d: uptime.slice(-90) }).where(eq(components.id, componentId));
}

export const POST: APIRoute = async ({ request }) => {
  const secret = process.env.UPTIME_HOOK_SECRET;
  if (!secret) return json({ error: { code: 'not_configured', message: 'Prober not configured.' } }, 503);

  const headerSecret = request.headers.get('X-Status-Probe-Secret') ?? '';
  const querySecret = new URL(request.url).searchParams.get('key') ?? '';
  if (!secretMatches(headerSecret || querySecret, secret)) {
    return json({ error: { code: 'unauthorized', message: 'Invalid or missing secret.' } }, 401);
  }

  let body: any;
  try { body = await request.json(); }
  catch { return json({ error: { code: 'bad_request', message: 'Body must be JSON.' } }, 400); }

  const probes = Array.isArray(body?.probes) ? body.probes : null;
  if (!probes || probes.length === 0) {
    return json({ error: { code: 'bad_request', message: 'Expected { probes: [{ component, up }] }.' } }, 400);
  }

  const useOwner = pulpOwnerRouteFamilyConfigured('ingest');
  const ownerRunKey = request.headers.get('Idempotency-Key')?.trim() || String(Math.floor(Date.now() / 300_000));
  let projection: OwnerProjection | null = null;
  let ownerSource: OwnerProjection['sources'][number] | undefined;
  if (useOwner) {
    try { projection = await ownerProjection(); ownerSource = projection.sources.find((source) => source.name === 'Status Prober' && source.kind === 'probe' && !source.revoked && source.direct_targets); }
    catch (error) { return ownerFailure(error); }
    if (!ownerSource) return json({ error: { code: 'owner_unavailable', message: 'Status Prober owner source is not migrated for direct targets.' } }, 503);
  }
  const source = useOwner ? null : await getOrCreateAdapterSource('Status Prober', 'probe');
  const accepted: Array<{ component: string; signal?: Signal; skipped?: string }> = [];

  for (const p of probes) {
    const componentId = typeof p?.component === 'string' ? p.component.trim() : '';
    if (!componentId) { continue; }
    const signal: Signal = p?.up === false ? 'down' : 'ok';
    if (useOwner) {
      const component = projection!.components.find((entry) => entry.component.id === componentId)?.component;
      if (!component || component.archived) { accepted.push({ component: componentId, skipped: 'not_found' }); continue; }
      try { await ownerIngest(ownerSource!.id, componentId, signal, ownerBar(component.uptime_90d, SIGNAL_TO_BAR[signal]), ownerRunKey); }
      catch (error) { return ownerFailure(error); }
      accepted.push({ component: componentId, signal }); continue;
    }
    const rows = await db.select().from(components).where(eq(components.id, componentId));
    const svc = rows[0];
    if (!svc || svc.archivedAt != null) { accepted.push({ component: componentId, skipped: 'not_found' }); continue; }
    await bump90d(componentId, SIGNAL_TO_BAR[signal]);
    const before = await snapshotComponent(componentId);
    await appendObservation({
      sourceId: source!.id,
      componentId,
      signal,
      detail: 'status-probe',
      defaultTtlSeconds: STATUS_PROBE_TTL_SECONDS,
    });
    await notifyForComponent(componentId, before);
    accepted.push({ component: componentId, signal });
  }

  return json({ data: { accepted } }, 202);
};
