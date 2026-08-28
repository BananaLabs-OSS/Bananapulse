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
import { randomUUID } from 'node:crypto';
import { resolveSourceByToken, resolveTarget } from '../../../lib/sources';
import { appendObservation, type Signal } from '../../../lib/quorum';
import { snapshotComponent, notifyForComponent } from '../../../lib/notify';
import {
  callPulpEvent,
  PulpBridgeError,
  pulpOwnerRequestID,
  pulpOwnerRouteFamilyConfigured,
} from '@/lib/pulp-bridge';

const VALID_SIGNALS: Signal[] = ['ok', 'degraded', 'down'];

function json(body: unknown, status: number) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const AUTH_VERSION = 'bananapulse.auth/v1';
const MONITOR_VERSION = 'monitor.v1';
const AUTH_SOURCE_VALIDATE_EVENT = 'bananapulse.host.auth.source-credential.validate.v1';
const MONITOR_INGEST_EVENT = 'bananapulse.monitor.ingest.authenticated.v1';

interface SourceCredentialValidation {
  valid: boolean;
  source_id?: string;
}

interface OwnerIngestResult {
  deduped?: boolean;
  evaluation?: {
    observation_id: string;
    component_id: string;
    state: string;
    level?: string;
    non_ok: number;
    sources: number;
    reduced_coverage: boolean;
  };
}

function ownerFailure(error: unknown, target?: string): Response {
  // The bridge's typed domain errors are already shaped for the public adapter.
  // Keep them intact instead of falling back to the legacy writer after an owner
  // command has been attempted; that would create split-brain state.
  if (error instanceof PulpBridgeError) {
    if (error.detail) {
      try {
        const body = JSON.parse(error.detail) as { error?: { code?: string } };
        // The owner intentionally does not echo raw labels in its sanitized
        // error envelope. This public compatibility adapter can safely restore
        // the legacy message from the already-validated request value.
        if (target && error.status === 422 && body.error?.code === 'unmapped_target') {
          return json({
            error: {
              code: 'unmapped_target',
              message: `No mapping for target "${target}" on this source. Add a source_target_map row.`,
            },
          }, 422);
        }
        return json(body, error.status);
      } catch {
        // The route never exposes a raw bridge diagnostic to the public API.
      }
    }
    const status = error.status >= 500 ? 503 : error.status;
    return json({ error: { code: 'owner_unavailable', message: 'Ingest owner is unavailable.' } }, status);
  }
  return json({ error: { code: 'owner_unavailable', message: 'Ingest owner is unavailable.' } }, 503);
}

function ownerIngestEnabled(): boolean {
  // Source-token validation is a private host→auth-owner call. Both gates must
  // be live before the public route leaves the legacy implementation.
  return pulpOwnerRouteFamilyConfigured('ingest') && pulpOwnerRouteFamilyConfigured('auth');
}

async function validateOwnerSource(token: string): Promise<string | null> {
  const result = await callPulpEvent<{
    version: string;
    request_id: string;
    token: string;
    validated_at: string;
  }, SourceCredentialValidation>(AUTH_SOURCE_VALIDATE_EVENT, {
    version: AUTH_VERSION,
    request_id: pulpOwnerRequestID('source-credential-validate', token),
    token,
    validated_at: new Date().toISOString(),
  });
  return result.valid && result.source_id ? result.source_id : null;
}

async function ingestWithOwner(input: {
  sourceId: string;
  target: string;
  signal: Signal;
  detail: string | null;
  expiresAt: Date | null;
}): Promise<OwnerIngestResult> {
  const now = Math.floor(Date.now() / 1000);
  return callPulpEvent(MONITOR_INGEST_EVENT, {
    version: MONITOR_VERSION,
    // The legacy core treats no two independently received generic readings as
    // the same event. A fresh command ID preserves that behaviour while still
    // allowing the host to safely retry this exact command if transport support
    // is added later.
    id: `ingest_${randomUUID()}`,
    kind: 'ingest_observation',
    at_unix: now,
    ingest: {
      observation_id: `observation_${randomUUID()}`,
      source_id: input.sourceId,
      raw_label: input.target,
      signal: input.signal,
      detail: input.detail ?? '',
      expires_at_unix: input.expiresAt ? Math.floor(input.expiresAt.getTime() / 1000) : 0,
    },
  });
}

export const POST: APIRoute = async ({ request }) => {
  // Bearer token.
  const auth = request.headers.get('authorization') ?? '';
  const token = auth.toLowerCase().startsWith('bearer ') ? auth.slice(7).trim() : '';
  if (!token) {
    return json({ error: { code: 'unauthorized', message: 'Missing bearer token.' } }, 401);
  }

  const useOwner = ownerIngestEnabled();
  let ownerSourceId: string | null = null;
  if (useOwner) {
    try {
      ownerSourceId = await validateOwnerSource(token);
    } catch (error) {
      return ownerFailure(error);
    }
    if (!ownerSourceId) {
      return json({ error: { code: 'unauthorized', message: 'Unknown or revoked source token.' } }, 401);
    }
  }

  const source = useOwner ? null : await resolveSourceByToken(token);
  if (!source && !useOwner) {
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

  if (useOwner) {
    try {
      const result = await ingestWithOwner({
        sourceId: ownerSourceId!,
        target,
        signal,
        detail,
        expiresAt,
      });
      const evaluation = result.evaluation;
      // A successful monitor command is required to include the compatibility
      // evaluation. Treat a malformed owner response as unavailable instead of
      // inventing a public state or falling back to Postgres.
      if (!evaluation) {
        return json({ error: { code: 'owner_unavailable', message: 'Ingest owner returned an invalid response.' } }, 503);
      }
      return json({
        data: {
          observation_id: evaluation.observation_id,
          component_id: evaluation.component_id,
          state: evaluation.state,
          level: evaluation.level || null,
          non_ok: evaluation.non_ok,
          sources: evaluation.sources,
          reduced_coverage: evaluation.reduced_coverage,
        },
      }, 202);
    } catch (error) {
      return ownerFailure(error, target);
    }
  }

  // The owner branch returned above. This explicit narrowing keeps the legacy
  // path's source identity entirely separate from an asserted owner identity.
  if (!source) {
    return json({ error: { code: 'owner_unavailable', message: 'Ingest owner is unavailable.' } }, 503);
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
