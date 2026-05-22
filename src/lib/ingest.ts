// ingest.ts — client-side fetch + mapper for upstream status sources.
//
// Pure static engine: this code runs in the *browser*, not the server.
// It's imported by the <script type="module"> block in routes/index.astro
// and called every 30s to refresh the page without a navigation.
//
// v0.1 ships ONE example mapper, `queue-status`, that expects a JSON
// response shaped around server-pool capacity (active/max units, queue
// length, dependency health flags). Consumers with a different upstream
// shape add a new `type` value plus a matching mapper here. The mapper
// boundary is the only place that knows about upstream-specific field
// names.
//
// No timeouts via AbortController.signal here — the browser already
// caps fetches; if a probe takes too long the next 30s interval just
// supersedes it. Failed fetches return an unreachable status which the
// renderer turns into a `major` outage tile.

import { rollupOverall } from './reduce.js';
import type { CanonicalStatus, Health, Subsystem } from './canonical.js';

export interface ClientSource {
  url: string;
  type: 'queue-status';
}

interface QueueStatusUpstream {
  active_servers: number;
  max_servers: number;
  queue_length: number;
  max_queue: number;
  used_cpu: number;
  used_memory: number;
  capacity_full: boolean;
  // Dependency-health fields. Bananapulse reads the generic-named fields
  // first, then falls back to Sessions-shaped legacy names so existing
  // sessions.gg consumers work without changing their upstream.
  provisioner_status?: string;
  payments_status?: string;
  email_status?: string;
  bananagine_status?: string;
  stripe_status?: string;
  resend_status?: string;
  estimated_next_slot?: string;
  estimated_new_order?: string;
  // Optional: when true, status page renders maintenance banner + flips
  // overall to `maintenance`. Consumers set this on their feed when
  // planned maintenance is on (Sessions wires it from MAINTENANCE_MODE).
  maintenance_mode?: boolean;
}

function mapDependencyHealth(raw: string): Health {
  switch (raw) {
    case 'reachable':
    case 'ok':
    case 'healthy':
      return 'operational';
    case 'degraded':
    case 'slow':
      return 'degraded';
    case 'unreachable':
    case 'down':
    case 'error':
      return 'major';
    default:
      return 'degraded';
  }
}

function mapCapacityHealth(used: number, full: boolean): Health {
  if (full) return 'degraded';
  if (used >= 90) return 'degraded';
  return 'operational';
}

function mapQueueStatus(raw: QueueStatusUpstream): CanonicalStatus {
  // Planned maintenance short-circuits the dependency-health view: the
  // operator knows the site is intentionally degraded, so we render that
  // signal cleanly instead of a screen full of red dependency tiles.
  if (raw.maintenance_mode) {
    return {
      overall: 'maintenance',
      subsystems: [
        {
          name: 'Planned maintenance',
          status: 'maintenance',
          message: 'Sessions is in planned maintenance. Existing servers continue to run; new orders and reconfigurations are paused.',
        },
      ],
      updatedAt: new Date().toISOString(),
      banner: {
        tone: 'maintenance',
        text: 'Planned maintenance in progress.',
      },
    };
  }

  // Capacity status is the rollup of CPU + RAM usage against thresholds,
  // and the explicit capacity_full flag. Resource percentages themselves
  // are telemetry, not status — a status page answers "is it up?" not
  // "what's the load?" — so meters intentionally omitted.
  const capacityHealth = mapCapacityHealth(
    Math.max(raw.used_cpu, raw.used_memory),
    raw.capacity_full,
  );

  const subsystems: Subsystem[] = [
    {
      name: 'Server capacity',
      status: capacityHealth,
      message: raw.capacity_full
        ? `Capacity full — ${raw.queue_length} in queue`
        : `${raw.active_servers}/${raw.max_servers} units active`,
    },
    { name: 'Provisioner', status: mapDependencyHealth(raw.provisioner_status ?? raw.bananagine_status ?? '') },
    { name: 'Payments', status: mapDependencyHealth(raw.payments_status ?? raw.stripe_status ?? '') },
    { name: 'Email delivery', status: mapDependencyHealth(raw.email_status ?? raw.resend_status ?? '') },
  ];

  const overall = rollupOverall(subsystems);

  const banner =
    raw.capacity_full && raw.estimated_next_slot
      ? {
          tone: 'degraded' as Health,
          text: `Capacity full — next slot estimated ${new Date(raw.estimated_next_slot).toUTCString()}`,
        }
      : undefined;

  return {
    overall,
    subsystems,
    updatedAt: new Date().toISOString(),
    banner,
  };
}

export function unreachableStatus(sourceName: string): CanonicalStatus {
  return {
    overall: 'major',
    subsystems: [
      {
        name: sourceName,
        status: 'major',
        message: 'Upstream status endpoint unreachable',
      },
    ],
    updatedAt: new Date().toISOString(),
    banner: {
      tone: 'major',
      text: 'Status data unavailable. Check back in 30 seconds.',
    },
  };
}

/**
 * Fetch a source from the browser, map it to canonical status.
 * Returns an unreachable status on any failure (network / non-2xx /
 * parse / CORS). The caller renders unreachable as a `major` outage.
 */
export async function fetchStatus(
  source: ClientSource,
  siteName: string,
): Promise<CanonicalStatus> {
  let upstream: QueueStatusUpstream | null = null;
  try {
    const res = await fetch(source.url, {
      method: 'GET',
      headers: { Accept: 'application/json' },
    });
    if (!res.ok) return unreachableStatus(siteName);
    upstream = (await res.json()) as QueueStatusUpstream;
  } catch {
    return unreachableStatus(siteName);
  }
  switch (source.type) {
    case 'queue-status':
      return mapQueueStatus(upstream);
    default:
      return unreachableStatus(siteName);
  }
}
