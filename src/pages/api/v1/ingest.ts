/**
 * POST /api/v1/ingest   Authorization: Bearer <source-token>
 *
 * The ONE inbound shape for the source-agnostic core. Body:
 *   { target, signal, detail?, expires_at? }
 *
 * - `source` is resolved from the bearer token (body value, if any, is a hint).
 * - `target` is a raw label resolved to a component via source_target_map.
 * - An append-only observation is written and quorum runs for the component.
 * - Unknown / revoked tokens are rejected with 401.
 *
 * Vendor-specific fixed payloads never hit this route directly; they hit a
 * thin adapter (e.g. /api/v1/ingest/uptimerobot) that translates to this
 * same core path via appendObservation().
 */
import type { APIRoute } from 'astro';
import { resolveSourceByToken, resolveTarget } from '../../../lib/sources';
import { appendObservation, type Signal } from '../../../lib/quorum';
import { snapshotComponent, notifyForComponent } from '../../../lib/notify';

const VALID_SIGNALS: Signal[] = ['ok', 'degraded', 'down'];

function json(body: unknown, status: number) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

export const POST: APIRoute = async ({ request }) => {
  // Bearer token.
  const auth = request.headers.get('authorization') ?? '';
  const token = auth.toLowerCase().startsWith('bearer ') ? auth.slice(7).trim() : '';
  if (!token) {
    return json({ error: { code: 'unauthorized', message: 'Missing bearer token.' } }, 401);
  }

  const source = await resolveSourceByToken(token);
  if (!source) {
    return json({ error: { code: 'unauthorized', message: 'Unknown or revoked source token.' } }, 401);
  }

  // Body.
  let body: any;
  try {
    body = await request.json();
  } catch {
    return json({ error: { code: 'bad_request', message: 'Body must be JSON.' } }, 400);
  }

  const target = typeof body?.target === 'string' ? body.target.trim() : '';
  const signal = body?.signal as Signal;
  const detail = typeof body?.detail === 'string' ? body.detail : null;

  if (!target) {
    return json({ error: { code: 'bad_request', message: '`target` is required.' } }, 400);
  }
  if (!VALID_SIGNALS.includes(signal)) {
    return json({ error: { code: 'bad_request', message: `\`signal\` must be one of: ${VALID_SIGNALS.join(', ')}.` } }, 400);
  }

  // expires_at: optional ISO timestamp enabling the dead-man for this source.
  let expiresAt: Date | null = null;
  if (body?.expires_at != null) {
    const d = new Date(body.expires_at);
    if (isNaN(d.getTime())) {
      return json({ error: { code: 'bad_request', message: '`expires_at` must be a valid ISO timestamp.' } }, 400);
    }
    expiresAt = d;
  }

  // Resolve raw label -> component (the only place vendor vocab touches the model).
  const componentId = await resolveTarget(source.id, target);
  if (!componentId) {
    return json({
      error: {
        code: 'unmapped_target',
        message: `No mapping for target "${target}" on this source. Add a source_target_map row.`,
      },
    }, 422);
  }

  // Snapshot open-incident state pre-engine so we can narrate the exact
  // lifecycle transition (open/update/resolve) after quorum reconciles.
  const before = await snapshotComponent(componentId);
  const { observationId, evaluation } = await appendObservation({
    sourceId: source.id,
    componentId,
    signal,
    detail,
    expiresAt,
    defaultTtlSeconds: source.defaultTtl ?? null,
  });
  await notifyForComponent(componentId, before);

  return json({
    data: {
      observation_id: observationId,
      component_id: componentId,
      state: evaluation.state,           // ok | watch | declared
      level: evaluation.level,           // null | degraded | major
      non_ok: evaluation.nonOkCount,
      sources: evaluation.totalSources,
      reduced_coverage: evaluation.reducedCoverage,
    },
  }, 202);
};
