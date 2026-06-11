/**
 * GET /api/v1/summary.json[?scope=sessions]
 *
 * The data contract: the whole component subtree under the scope's landing
 * root, with rolled-up (quorum-derived) status + attached incidents per node,
 * so the front end can drill client-side with no round-trips. Also powers the
 * marketing sites' "● Live now" badge (reads `status` / `live`). Cacheable.
 */
import type { APIRoute } from 'astro';
import { buildSummaryTree } from '../../../lib/components';
import { statusToState } from '../../../lib/types';

const headers = {
  'Content-Type': 'application/json',
  'Cache-Control': 'public, max-age=30',
  'Access-Control-Allow-Origin': '*',
};

function body(scope: string | null, tree: unknown, status: 'operational' | 'degraded' | 'outage' | 'maintenance') {
  return JSON.stringify({
    scope,
    status,
    state: statusToState(status),
    live: true,
    tree,
    generatedAt: new Date().toISOString(),
  }, null, 2);
}

/**
 * Dead-man discipline: on a DB/derivation error we must NEVER synthesize
 * 'operational'. Report 'unknown' + live:false so the marketing "● Live now"
 * badge fails CLOSED rather than claiming health we can no longer verify.
 */
function unknownBody(scope: string | null) {
  return JSON.stringify({
    scope,
    status: 'unknown',
    state: 'unknown',
    live: false,
    tree: null,
    generatedAt: new Date().toISOString(),
  }, null, 2);
}

export const GET: APIRoute = async ({ locals, url }) => {
  const qScope = url.searchParams.get('scope');
  const scope = qScope || ((locals as any).scope as string | null) || null;
  try {
    const root = await buildSummaryTree(scope);
    // A null root (scope's landing-root missing/archived/wrong-scope/partial
    // seed) is a data-integrity failure, not an "all clear" — fail CLOSED past
    // the catch so we never emit 'operational'+live:true for a vanished tree.
    if (!root) return new Response(unknownBody(scope), { status: 503, headers });
    return new Response(body(scope, root, root.status), { status: 200, headers });
  } catch (_e) {
    // DB/derivation failure: fail CLOSED. Never claim 'operational' on error —
    // emit 'unknown' + live:false with a 503 so callers (and the "● Live now"
    // badge) treat us as unverifiable, mirroring the engine's dead-man rule.
    return new Response(unknownBody(scope), { status: 503, headers });
  }
};
