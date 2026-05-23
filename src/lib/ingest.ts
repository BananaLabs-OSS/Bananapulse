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
import {
  assertCanonicalStatus,
  type CanonicalStatus,
  type Health,
  type Incident,
  type Subsystem,
} from './canonical.js';

export interface ClientSource {
  url: string;
  /** `queue-status` is the v0.1 capacity/dependency-flags mapper. `canonical`
   *  is the pre-canonicalized contract — the upstream already returns the
   *  full canonical shape (overall/components/incidents/maintenance) and
   *  Bananapulse just passes it through with light validation. */
  type: 'queue-status' | 'canonical';
}

/** The pre-canonicalized API response shape. Sessions's Evolution
 *  backend serves this at /api/status. Keys/types must stay in lockstep
 *  with that contract — see the Evolution status renderer for the spec. */
export interface CanonicalApiResponse {
  overall: Health;
  components: CanonicalApiComponent[];
  incidents: Incident[];
  maintenance: Incident[];
  updatedAt: string;
}

export interface CanonicalApiComponent {
  id: number;
  slug: string;
  name: string;
  status: Health;
  message?: string | null;
  children?: CanonicalApiComponent[];
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

/** Map a CanonicalApiComponent (API shape) to a Subsystem (renderer shape).
 *  Carries id + slug through as optional extras (not in the Subsystem type
 *  but valid at runtime) — these are needed by the inline-script renderer
 *  to look up `affectedComponents` slugs → component names for the
 *  incident-affects chip line. Dropping them silently breaks that lookup.
 *  (Pre-M8 the lookup was by integer id; the canonical wire now ships
 *  stable slug strings.) */
function componentToSubsystem(c: CanonicalApiComponent): Subsystem {
  return {
    name: c.name,
    status: c.status,
    message: c.message ?? undefined,
    children: c.children?.map(componentToSubsystem),
    // Extra runtime fields preserved for the incident-affects lookup.
    ...({ id: c.id, slug: c.slug } as Partial<Subsystem>),
  };
}

/** Derive a top-of-page banner from active incidents. Highest severity wins.
 *  Returns undefined when no actionable banner is appropriate (no active
 *  incidents AND overall is operational). For overall=maintenance with no
 *  incidents we still synthesise a maintenance banner so the page reads
 *  cleanly when the operator flipped a maintenance switch upstream. */
function deriveBanner(
  overall: Health,
  activeIncidents: Incident[],
  activeMaintenance: Incident[],
): { tone: Health; text: string } | undefined {
  // Severity rank for active-incident-banner selection. Higher = worse.
  const rank: Record<Health, number> = { operational: 0, maintenance: 1, degraded: 2, major: 3 };
  const allActive = [...activeIncidents, ...activeMaintenance];
  if (allActive.length > 0) {
    const top = allActive.reduce((best, cur) =>
      rank[cur.severity] > rank[best.severity] ? cur : best,
    );
    // Verb depends on severity: "Investigating" for active outages,
    // "In progress" for maintenance windows. Operator can override the
    // wording via the incident title itself; this prefix is just a hint.
    const prefix = top.severity === 'maintenance' ? 'In progress' : 'Investigating';
    return { tone: top.severity, text: `${prefix}: ${top.title}` };
  }
  if (overall === 'maintenance') {
    return { tone: 'maintenance', text: 'Scheduled maintenance underway.' };
  }
  return undefined;
}

/** Light validation + passthrough for the pre-canonicalized API contract.
 *  Trusts the upstream's components/incidents shape (it's our own backend
 *  in the Sessions case) but still runs assertCanonicalStatus on the
 *  subsystems tree so a schema regression surfaces as an unreachable
 *  rather than a render crash. */
function mapCanonical(raw: CanonicalApiResponse): CanonicalStatus {
  const subsystems = (raw.components ?? []).map(componentToSubsystem);
  const updatedAt = typeof raw.updatedAt === 'string' && raw.updatedAt
    ? raw.updatedAt
    : new Date().toISOString();
  const candidate: CanonicalStatus = {
    overall: raw.overall,
    subsystems,
    updatedAt,
  };
  // Throws if overall/subsystems are malformed — caller catches and
  // surfaces unreachable. We do NOT validate incidents here because the
  // renderer tolerates partial fields (missing body, etc.) gracefully.
  assertCanonicalStatus(candidate);

  const activeIncidents = (raw.incidents ?? []).filter((i) => !i.resolvedAt);
  const activeMaintenance = (raw.maintenance ?? []).filter((i) => !i.resolvedAt);
  const banner = deriveBanner(raw.overall, activeIncidents, activeMaintenance);
  return banner ? { ...candidate, banner } : candidate;
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

/** Result of fetchStatus — includes incident lists so the renderer can
 *  surface live incidents (canonical sources only; queue-status returns
 *  empty arrays and the page falls back to the build-time incidents.json
 *  read for that case). */
export interface FetchStatusResult {
  status: CanonicalStatus;
  /** Active + resolved incidents from the upstream. Empty for non-canonical
   *  sources — those use the build-time incidents.json fallback instead. */
  incidents: Incident[];
  /** Planned maintenance windows from the upstream. Empty for non-canonical. */
  maintenance: Incident[];
}

/**
 * Fetch a source from the browser, map it to canonical status.
 * Returns an unreachable status on any failure (network / non-2xx /
 * parse / CORS). The caller renders unreachable as a `major` outage.
 *
 * The returned shape carries incidents + maintenance so canonical-source
 * consumers can render live incidents without a second fetch. Non-canonical
 * sources (e.g. queue-status) return empty arrays here and the renderer
 * relies on the build-time incidents.json read instead.
 */
export async function fetchStatus(
  source: ClientSource,
  siteName: string,
): Promise<FetchStatusResult> {
  let upstream: unknown = null;
  try {
    const res = await fetch(source.url, {
      method: 'GET',
      headers: { Accept: 'application/json' },
    });
    if (!res.ok) {
      return { status: unreachableStatus(siteName), incidents: [], maintenance: [] };
    }
    upstream = await res.json();
  } catch {
    return { status: unreachableStatus(siteName), incidents: [], maintenance: [] };
  }
  switch (source.type) {
    case 'queue-status':
      return {
        status: mapQueueStatus(upstream as QueueStatusUpstream),
        incidents: [],
        maintenance: [],
      };
    case 'canonical': {
      try {
        const raw = upstream as CanonicalApiResponse;
        const status = mapCanonical(raw);
        return {
          status,
          incidents: Array.isArray(raw.incidents) ? raw.incidents : [],
          maintenance: Array.isArray(raw.maintenance) ? raw.maintenance : [],
        };
      } catch {
        // Schema regression / missing required fields → unreachable.
        // Keeps the page renderable instead of throwing on poll.
        return { status: unreachableStatus(siteName), incidents: [], maintenance: [] };
      }
    }
    default:
      return { status: unreachableStatus(siteName), incidents: [], maintenance: [] };
  }
}
