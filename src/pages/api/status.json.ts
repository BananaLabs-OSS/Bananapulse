import type { APIRoute } from 'astro';
import { getPublicLeafComponents, buildSummaryTree } from '../../lib/components';
import { getActiveIncidents } from '../../lib/db-incidents';
import { getUpcomingMaintenance } from '../../lib/db-maintenance';
import { worstStatus, statusToState } from '../../lib/types';

export const GET: APIRoute = async ({ locals }) => {
  const scope = (locals as any).scope as string | null;

  try {
    // Derive from the components tree so this AGREES with /api/v1/summary.json.
    const services = await getPublicLeafComponents(scope || null);
    // Fold the root's effective (worst-of-subtree) status in as the floor so an
    // incident declared directly on a non-leaf product/org node — which never
    // appears in the leaf list — still moves `overall`. Keeps status.json in
    // agreement with summary.json/HTML regardless of which node kind is DECLARED.
    const root = await buildSummaryTree(scope || null);
    const overall = worstStatus([
      ...(services as any),
      ...(root ? [{ status: root.status }] : []),
    ] as any);
    const activeIncidents = await getActiveIncidents(scope || undefined);
    const maintenances = await getUpcomingMaintenance(scope || undefined);

    const body = {
      status: overall,
      state: statusToState(overall),
      services: services.map((s) => ({
        id: s.id,
        name: s.name,
        product: s.product,
        status: s.status,
      })),
      activeIncidents: activeIncidents.map((i) => ({
        id: i.id,
        title: i.title,
        severity: i.severity,
        status: i.status,
        started: i.started,
      })),
      scheduledMaintenance: maintenances.map((m) => ({
        id: m.id,
        title: m.title,
        scheduledStart: m.scheduledStart,
        scheduledEnd: m.scheduledEnd,
      })),
    };

    return new Response(JSON.stringify(body, null, 2), {
      status: 200,
      headers: {
        'Content-Type': 'application/json',
        'Cache-Control': 'public, max-age=30',
        'Access-Control-Allow-Origin': '*',
      },
    });
  } catch (_e) {
    // DB/derivation failure: fail CLOSED. Never synthesize 'operational' on an
    // error — emit 'unknown' + live:false with a 503 so badges/monitors treat us
    // as unverifiable, mirroring summary.json's dead-man rule.
    const body = {
      status: 'unknown',
      state: 'unknown',
      live: false,
      services: [],
      activeIncidents: [],
      scheduledMaintenance: [],
    };

    return new Response(JSON.stringify(body, null, 2), {
      status: 503,
      headers: {
        'Content-Type': 'application/json',
        'Cache-Control': 'public, max-age=30',
        'Access-Control-Allow-Origin': '*',
      },
    });
  }
};
