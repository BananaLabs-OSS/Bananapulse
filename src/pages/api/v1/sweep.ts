/**
 * POST /api/v1/sweep — run the quorum engine across every known component.
 *
 * This is the timer/dead-man path: TTL expiry (a source going silent past its
 * expires_at / default_ttl) only changes the derived state when something
 * re-evaluates the component. New observations re-evaluate on arrival; this
 * sweep re-evaluates everything on a schedule so a silent source flips
 * coverage and any auto-incident state settles even with no inbound traffic.
 *
 * Wire a Netlify scheduled function (or any cron) to POST here. Protected by
 * the same UPTIME_HOOK_SECRET shared secret to keep it internal.
 */
import type { APIRoute } from 'astro';
import { timingSafeEqual } from 'node:crypto';
import { sweepQuorum } from '@/lib/quorum';
import { snapshotAllOpenIncidents, notifyForComponent } from '@/lib/notify';

export const POST: APIRoute = async ({ request }) => {
  const secret = process.env.UPTIME_HOOK_SECRET;
  if (!secret) {
    return new Response(JSON.stringify({ error: { code: 'not_configured', message: 'Sweep not configured.' } }), { status: 503 });
  }
  const provided = request.headers.get('X-Uptime-Hook-Secret') ?? '';
  if (provided.length !== secret.length || !timingSafeEqual(Buffer.from(provided), Buffer.from(secret))) {
    return new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'Invalid secret.' } }), { status: 401 });
  }

  try {
    // Snapshot open-incident state BEFORE the sweep reconciles, so dead-man
    // transitions (e.g. an auto-incident resolving because a source went stale
    // and quorum dropped below 2) can be narrated to subscribers.
    const before = await snapshotAllOpenIncidents();

    const evals = await sweepQuorum();
    for (const e of evals) {
      const snap = before.get(e.componentId) ?? { incidentId: null, status: null, severity: null };
      await notifyForComponent(e.componentId, snap);
    }

    const declared = evals.filter((e) => e.state === 'declared').length;
    const watch = evals.filter((e) => e.state === 'watch').length;
    const reducedCoverage = evals.filter((e) => e.reducedCoverage).length;
    return new Response(JSON.stringify({
      data: { components: evals.length, declared, watch, reducedCoverage },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  } catch {
    return new Response(JSON.stringify({ error: { code: 'server_error', message: 'Sweep failed.' } }), { status: 500 });
  }
};
