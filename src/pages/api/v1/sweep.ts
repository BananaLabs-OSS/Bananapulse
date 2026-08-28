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
import { randomUUID, timingSafeEqual } from 'node:crypto';
import { sweepQuorum } from '@/lib/quorum';
import { snapshotAllOpenIncidents, notifyForComponent } from '@/lib/notify';
import { callPulpEvent, PulpBridgeError, pulpOwnerRouteFamilyConfigured } from '@/lib/pulp-bridge';

const MONITOR_SWEEP_EVENT = 'bananapulse.monitor.sweep.v1';

interface OwnerSweepResult {
  sweep?: {
    components: number;
    declared: number;
    watch: number;
    reduced_coverage: number;
  };
}

function json(body: unknown, status: number) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

function ownerFailure(error: unknown): Response {
  // Never call sweepQuorum after an owner attempt: replaying the same timer in
  // the legacy writer could create a divergent incident/notification history.
  if (error instanceof PulpBridgeError && error.detail) {
    try {
      return json(JSON.parse(error.detail), error.status);
    } catch {
      // Public routes deliberately do not expose raw bridge diagnostics.
    }
  }
  const status = error instanceof PulpBridgeError && error.status < 500 ? error.status : 503;
  return json({ error: { code: 'owner_unavailable', message: 'Sweep owner is unavailable.' } }, status);
}

export const POST: APIRoute = async ({ request }) => {
  const secret = process.env.UPTIME_HOOK_SECRET;
  if (!secret) {
    return new Response(JSON.stringify({ error: { code: 'not_configured', message: 'Sweep not configured.' } }), { status: 503 });
  }
  const provided = request.headers.get('X-Uptime-Hook-Secret') ?? '';
  if (provided.length !== secret.length || !timingSafeEqual(Buffer.from(provided), Buffer.from(secret))) {
    return new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'Invalid secret.' } }), { status: 401 });
  }

  if (pulpOwnerRouteFamilyConfigured('sweep')) {
    try {
      // The Lua event commits monitor reconciliation then applies subscriber
      // transition delivery intents. A fresh command ID preserves the legacy
      // scheduler semantics: two separately received ticks are two sweeps.
      const result = await callPulpEvent<{
        version: string;
        id: string;
        kind: string;
        at_unix: number;
      }, OwnerSweepResult>(MONITOR_SWEEP_EVENT, {
        version: 'monitor.v1',
        id: `sweep_${randomUUID()}`,
        kind: 'sweep_reconcile',
        at_unix: Math.floor(Date.now() / 1000),
      });
      if (!result.sweep) {
        return json({ error: { code: 'owner_unavailable', message: 'Sweep owner returned an invalid response.' } }, 503);
      }
      return json({
        data: {
          components: result.sweep.components,
          declared: result.sweep.declared,
          watch: result.sweep.watch,
          reducedCoverage: result.sweep.reduced_coverage,
        },
      }, 200);
    } catch (error) {
      return ownerFailure(error);
    }
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
