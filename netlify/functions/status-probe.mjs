/**
 * Netlify scheduled function — the status page's OWN external prober.
 *
 * Runs on Netlify (a failure domain independent of your origin and of any
 * external monitor like UptimeRobot), so it's a SECOND external vantage. When a
 * box dies and the on-box eyes go dark, this + another external can AGREE → the
 * quorum CONFIRMS the outage instead of stalling at one vote.
 *
 * It GET-probes your customer-facing endpoints (free outbound fetches), then POSTs
 * ONE batch to /api/v1/ingest/status-probe?key=<UPTIME_HOOK_SECRET>, which feeds
 * the core engine (lazy 'Status Prober' source, appendObservation, notify).
 *
 * Cost: 1 invocation + 1 ingest POST every 5 min ≈ 17k invocations/month — a few
 * % of the free-tier function cap, ZERO build minutes. It probes EXTERNAL services
 * only (never an SSR route, never itself).
 *
 * Required env (Netlify, Functions/Runtime scope):
 *   UPTIME_HOOK_SECRET    — reused; the status-probe adapter validates it
 *   PUBLIC_STATUS_URL     — your status site base, e.g. https://status.example.com
 *   STATUS_PROBE_TARGETS  — JSON array of { "component": "<id>", "url": "<url>" }
 *                           e.g. [{"component":"backend","url":"https://api.example.com/health"}]
 *
 * Each target's `component` is a component id (the prober knows its own targets).
 * Edit STATUS_PROBE_TARGETS to change coverage — no code change, no redeploy of logic.
 */

export const config = { schedule: '*/5 * * * *' };

function loadTargets() {
  try {
    const t = JSON.parse(process.env.STATUS_PROBE_TARGETS || '[]');
    return Array.isArray(t) ? t.filter((x) => x && x.component && x.url) : [];
  } catch {
    return [];
  }
}

async function probe(url) {
  try {
    const res = await fetch(url, { method: 'GET', redirect: 'follow', signal: AbortSignal.timeout(8000) });
    return res.ok || (res.status >= 200 && res.status < 400);
  } catch {
    return false;
  }
}

export default async function handler() {
  const secret = process.env.UPTIME_HOOK_SECRET;
  if (!secret) return new Response('UPTIME_HOOK_SECRET not set', { status: 503 });
  const base = (process.env.PUBLIC_STATUS_URL || '').replace(/\/$/, '');
  if (!base) return new Response('PUBLIC_STATUS_URL not set', { status: 503 });

  const targets = loadTargets();
  if (targets.length === 0) return new Response('STATUS_PROBE_TARGETS empty — nothing to probe', { status: 200 });

  // Probe all targets concurrently — runtime stays ~one slow probe regardless of
  // how many you add, so it scales to a long list without hitting the time limit.
  const probes = await Promise.all(
    targets.map(async (t) => ({ component: t.component, up: await probe(t.url) })),
  );

  try {
    const res = await fetch(`${base}/api/v1/ingest/status-probe?key=${encodeURIComponent(secret)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ probes }),
      signal: AbortSignal.timeout(10000),
    });
    return new Response(`status-probe: ${res.status} ${JSON.stringify(probes)}`, { status: 200 });
  } catch (e) {
    return new Response(`status-probe ingest failed: ${e}`, { status: 502 });
  }
}
