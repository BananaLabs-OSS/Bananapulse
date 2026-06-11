/**
 * Source registry + token resolution for the source-agnostic core engine.
 *
 * A `source` is an independent vantage that POSTs observations. The bearer
 * token in the ingest request is sha256-hashed and matched against
 * `sources.token_hash`. Raw labels are resolved to components via
 * `source_target_map` — the only place vendor vocabulary touches the model.
 *
 * Reuses the existing sha256 token primitives from api-tokens.ts so there is
 * one hashing scheme across the system.
 */
import { db } from '@/db';
import { sources, sourceTargetMap } from '@/db/schema';
import { eq, and, isNull } from 'drizzle-orm';
import { nanoid } from 'nanoid';
import { hashToken, generateToken } from './api-tokens';
import { componentExists } from './components';

export type SourceKind = 'push' | 'probe' | 'heartbeat' | 'manual';

/** Resolve a bearer token to an active (non-revoked) source, or null. */
export async function resolveSourceByToken(token: string) {
  const hash = hashToken(token);
  const rows = await db.select().from(sources)
    .where(and(eq(sources.tokenHash, hash), isNull(sources.revokedAt)));
  return rows[0] ?? null;
}

/** Register a new source and return the one-time plaintext token. */
export async function createSource(opts: {
  name: string;
  kind?: SourceKind;
  weight?: number;
  defaultTtl?: number | null;
  // First-party trust: a trusted source declares on its own (capped to
  // degraded) instead of sitting in WATCH. External validators stay untrusted.
  trusted?: boolean;
}) {
  const raw = generateToken();
  const id = nanoid();
  await db.insert(sources).values({
    id,
    name: opts.name,
    tokenHash: hashToken(raw),
    kind: opts.kind ?? 'push',
    weight: opts.weight ?? 1,
    trusted: opts.trusted ?? false,
    defaultTtl: opts.defaultTtl ?? null,
  });
  return { id, token: raw, name: opts.name };
}

export async function listSources() {
  return db.select().from(sources).orderBy(sources.createdAt);
}

export async function revokeSource(id: string) {
  await db.update(sources).set({ revokedAt: new Date() }).where(eq(sources.id, id));
}

/**
 * Resolve a raw label to a component id for a given source.
 * Falls back to using the raw label directly as a component id if no mapping
 * row exists — this lets sources you control name components canonically
 * without a mapping row, while vendor labels still go through the map.
 */
export async function resolveTarget(sourceId: string, rawLabel: string): Promise<string | null> {
  const rows = await db.select().from(sourceTargetMap)
    .where(and(eq(sourceTargetMap.sourceId, sourceId), eq(sourceTargetMap.rawLabel, rawLabel)));
  const componentId = rows[0]?.componentId;
  // Single-model guard: a mapping may exist but point at a component that no
  // longer exists (or was archived) — never resolve to a non-existent id, or
  // an observation/incident would land on a target the public surface can't
  // see (the invisible-outage bug).
  if (componentId && (await componentExists(componentId))) return componentId;
  return null;
}

/**
 * Add (or replace) a raw_label → component mapping for a source. Upsert: one
 * mapping per (source, rawLabel), so re-mapping a label REPLACES it instead of
 * creating an ambiguous duplicate (resolveTarget reads the first match).
 */
export async function mapTarget(sourceId: string, rawLabel: string, componentId: string) {
  if (!(await componentExists(componentId))) {
    throw new Error(`Unknown component "${componentId}".`);
  }
  const existing = await db.select({ id: sourceTargetMap.id }).from(sourceTargetMap)
    .where(and(eq(sourceTargetMap.sourceId, sourceId), eq(sourceTargetMap.rawLabel, rawLabel)));
  if (existing[0]) {
    await db.update(sourceTargetMap).set({ componentId }).where(eq(sourceTargetMap.id, existing[0].id));
    return existing[0].id;
  }
  const id = nanoid();
  await db.insert(sourceTargetMap).values({ id, sourceId, rawLabel, componentId });
  return id;
}

/** Remove a raw_label → component mapping by its id. */
export async function removeMapping(id: string) {
  await db.delete(sourceTargetMap).where(eq(sourceTargetMap.id, id));
}

/**
 * Find (or lazily create) the special high-weight `manual` source that
 * human overrides flow through, so a manual override is just another
 * observation in the same engine. Idempotent.
 */
export async function getManualSource() {
  const rows = await db.select().from(sources).where(eq(sources.kind, 'manual'));
  if (rows[0]) return rows[0];
  const id = nanoid();
  // token_hash is unguessable/random — the manual source is never reached via /ingest,
  // only via admin actions, so it needs no usable token.
  await db.insert(sources).values({
    id,
    name: 'Manual override',
    tokenHash: hashToken(generateToken()),
    kind: 'manual',
    weight: 100,
    defaultTtl: null,
  });
  const created = await db.select().from(sources).where(eq(sources.id, id));
  return created[0];
}

/**
 * Find (or lazily create) a named adapter source by a stable name. Used by
 * thin vendor adapters (e.g. uptimerobot) so their translated
 * observations attribute to one canonical source row without manual setup.
 * The adapter has its own auth (a shared secret), so the source token is
 * never used directly via /ingest.
 */
export async function getOrCreateAdapterSource(name: string, kind: SourceKind = 'probe') {
  const rows = await db.select().from(sources).where(eq(sources.name, name));
  if (rows[0]) return rows[0];
  const id = nanoid();
  await db.insert(sources).values({
    id,
    name,
    tokenHash: hashToken(generateToken()),
    kind,
    weight: 1,
    defaultTtl: null,
  });
  const created = await db.select().from(sources).where(eq(sources.id, id));
  return created[0];
}
