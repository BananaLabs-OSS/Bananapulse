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

  const source = await getOrCreateAdapterSource('Status Prober', 'probe');
  const accepted: Array<{ component: string; signal?: Signal; skipped?: string }> = [];

  for (const p of probes) {
    const componentId = typeof p?.component === 'string' ? p.component.trim() : '';
    if (!componentId) { continue; }
    const signal: Signal = p?.up === false ? 'down' : 'ok';
    const rows = await db.select().from(components).where(eq(components.id, componentId));
    const svc = rows[0];
    if (!svc || svc.archivedAt != null) { accepted.push({ component: componentId, skipped: 'not_found' }); continue; }
    await bump90d(componentId, SIGNAL_TO_BAR[signal]);
    const before = await snapshotComponent(componentId);
    await appendObservation({
      sourceId: source.id,
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
