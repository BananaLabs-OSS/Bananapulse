/**
 * Netlify scheduled function — the autonomous dead-man / TTL sweep driver.
 *
 * Runs every minute and POSTs to the status app's own `POST /api/v1/sweep`
 * endpoint with its shared secret. The sweep re-evaluates every component
 * through the quorum engine so that a source going silent past its TTL flips
 * coverage and any auto-incident settles even when NO inbound observation
 * arrives — i.e. the page stays truthful during an outage with no human and no
 * new traffic. Notification fan-out for any resulting transition happens inside
 * the sweep endpoint itself.
 *
 * Cron: every 5 minutes. (Was every minute — but at ~2 invocations/min including
 * the downstream /api/v1/sweep call, that alone is ~86k function calls/month and
 * pushed the Netlify account past its free-tier 125k cap → the whole site 503'd
 * "usage_exceeded" 2026-06-18. TTLs are 10 min → 4.5 h, so a 5-min sweep loses no
 * meaningful timeliness while cutting the dominant invocation source ~5×.)
 * The schedule is declared via the exported `config.schedule` below — no extra
 * dependency or netlify.toml entry is required, only that this file lives under
 * `netlify/functions/`.
 *
 *   "*\/5 * * * *"  = every 5th minute
 *
 * Required env (set in Netlify): UPTIME_HOOK_SECRET (the sweep's shared secret).
 * Site origin is taken from Netlify's built-in URL/DEPLOY_URL env; if neither is
 * set the sweep is skipped (no hardcoded instance domain).
 */

export const config = {
  schedule: '*/5 * * * *',
};

export default async function handler() {
  const secret = process.env.UPTIME_HOOK_SECRET;
  if (!secret) {
    console.error('[sweep-cron] UPTIME_HOOK_SECRET not set; skipping sweep.');
    // 200 so Netlify does not mark the schedule as failing on a config gap.
    return new Response('sweep skipped: not configured', { status: 200 });
  }

  const origin = process.env.URL || process.env.DEPLOY_PRIME_URL || process.env.DEPLOY_URL || '';
  if (!origin) {
    console.error('[sweep-cron] no site origin env (URL/DEPLOY_URL); skipping sweep.');
    return new Response('sweep skipped: no origin', { status: 200 });
  }

  const target = `${origin.replace(/\/$/, '')}/api/v1/sweep`;

  try {
    const res = await fetch(target, {
      method: 'POST',
      headers: {
        'X-Uptime-Hook-Secret': secret,
        'Content-Type': 'application/json',
      },
      body: '{}',
    });
    const text = await res.text();
    if (!res.ok) {
      console.error(`[sweep-cron] sweep returned ${res.status}: ${text}`);
      return new Response(`sweep failed: ${res.status}`, { status: 200 });
    }
    console.log(`[sweep-cron] sweep ok: ${text}`);
    return new Response('sweep ok', { status: 200 });
  } catch (e) {
    console.error('[sweep-cron] sweep request failed', e);
    return new Response('sweep error', { status: 200 });
  }
}
