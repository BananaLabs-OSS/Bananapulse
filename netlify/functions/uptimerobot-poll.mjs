/**
 * Netlify scheduled function — UptimeRobot API poller.
 *
 * UptimeRobot paywalls *webhooks* (push) but its read-only *API* (pull) is free.
 * So instead of receiving a webhook, every 5 minutes we READ each monitor's
 * status via the free API and feed it into the SAME core engine the webhook
 * would have, by POSTing the webhook-shaped payload to
 *   /api/v1/ingest/uptimerobot?key=<UPTIME_HOOK_SECRET>
 * (reusing the adapter, secret, lazy source, and mapping already in place).
 *
 * The VANTAGE is still UptimeRobot's global probe network — off your origin, off any on-box monitor
 * — so this preserves an independent 3rd failure domain. This function is only
 * transport; it changes nothing about WHERE the observation is made.
 *
 * Required env (Netlify, Functions/Runtime scope):
 *   UPTIMEROBOT_API_KEY  — a read-only API key (UptimeRobot → My Settings → API)
 *   UPTIME_HOOK_SECRET   — the same secret the uptimerobot adapter validates
 *
 * Mapping: each monitor's numeric id is the raw target → map id → component in
 * the status admin. Until mapped, ingest returns 422 (a safe no-op) — expected.
 *
 * Cron: every 5 min (UptimeRobot free monitors check on ~5-min intervals, so
 * faster polling yields no new info). The adapter stamps a ~10-min dead-man TTL,
 * so one missed poll is tolerated; if this poller dies the vantage goes stale.
 */

export const config = { schedule: '*/5 * * * *' };

// UptimeRobot monitor status -> adapter alertType (1=down, 2=up). null = skip.
//   0 paused · 1 not-checked-yet · 2 up · 8 seems-down · 9 down
function statusToAlertType(status) {
  switch (Number(status)) {
    case 2: return 2;     // up
    case 8: return 1;     // seems down
    case 9: return 1;     // down
    default: return null; // paused / not-yet-checked → don't report
  }
}

export default async function handler() {
  const apiKey = process.env.UPTIMEROBOT_API_KEY;
  const secret = process.env.UPTIME_HOOK_SECRET;
  if (!apiKey || !secret) {
    console.error('[uptimerobot-poll] missing UPTIMEROBOT_API_KEY or UPTIME_HOOK_SECRET; skipping.');
    return new Response('skipped: not configured', { status: 200 });
  }

  const origin = (process.env.URL || process.env.DEPLOY_PRIME_URL || process.env.DEPLOY_URL || '').replace(/\/$/, '');
  if (!origin) {
    console.error('[uptimerobot-poll] no site origin env (URL/DEPLOY_URL); skipping.');
    return new Response('skipped: no origin', { status: 200 });
  }
  const ingest = `${origin}/api/v1/ingest/uptimerobot?key=${encodeURIComponent(secret)}`;

  let monitors = [];
  try {
    const res = await fetch('https://api.uptimerobot.com/v2/getMonitors', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'Cache-Control': 'no-cache' },
      body: `api_key=${encodeURIComponent(apiKey)}&format=json`,
    });
    const data = await res.json();
    if (data.stat !== 'ok') {
      console.error('[uptimerobot-poll] API error:', JSON.stringify(data.error || data));
      return new Response('uptimerobot api error', { status: 200 });
    }
    monitors = Array.isArray(data.monitors) ? data.monitors : [];
  } catch (e) {
    console.error('[uptimerobot-poll] fetch failed', e);
    return new Response('fetch failed', { status: 200 });
  }

  const nowSecs = Math.floor(Date.now() / 1000);
  let sent = 0, unmapped = 0, skipped = 0, errors = 0;

  for (const m of monitors) {
    const alertType = statusToAlertType(m.status);
    if (alertType === null) { skipped++; continue; }
    const payload = {
      monitorID: String(m.id),
      monitorFriendlyName: m.friendly_name ?? String(m.id),
      alertType: String(alertType),
      alertDateTime: String(nowSecs),
      alertDetails: `uptimerobot-poll status=${m.status}`,
    };
    try {
      const r = await fetch(ingest, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      if (r.status === 202) sent++;
      else if (r.status === 422) unmapped++;       // unmapped monitor — expected until mapped
      else { errors++; console.error(`[uptimerobot-poll] ingest ${r.status} for monitor ${m.id}`); }
    } catch (e) {
      errors++; console.error(`[uptimerobot-poll] ingest failed for monitor ${m.id}`, e);
    }
  }

  const msg = `[uptimerobot-poll] monitors=${monitors.length} sent=${sent} unmapped=${unmapped} skipped=${skipped} errors=${errors}`;
  console.log(msg);
  return new Response(msg, { status: 200 });
}
