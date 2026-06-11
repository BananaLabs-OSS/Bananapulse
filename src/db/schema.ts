import { pgTable, text, integer, timestamp, jsonb, boolean, index, type AnyPgColumn } from 'drizzle-orm/pg-core';

/* ──────────────────────────────────────────────────────────────
 * Components — the adjacency tree (Banana Pulse model).
 *
 * A single arbitrary-depth tree (`parent_id`) of organization → product →
 * service → host. Replaces the flat products/services split for the public
 * surface and the engine. A component id is what observations, incidents
 * (affects[]), and source_target_map reference. Current status is DERIVED
 * (quorum overlays the stored `status` fallback); a parent's effective status
 * bubbles up = worst of its subtree. Brand/domain live on product nodes.
 * ────────────────────────────────────────────────────────────── */
export const components = pgTable('components', {
  id: text('id').primaryKey(),
  parentId: text('parent_id').references((): AnyPgColumn => components.id),
  name: text('name').notNull(),
  // Structural kind for logic.
  kind: text('kind').notNull().default('service'), // organization | product | service | host
  // Descriptive display label (e.g. 'repo · runtime', 'Stripe', 'host').
  tag: text('tag'),
  // Stored fallback status; quorum-derived status overlays where observed.
  status: text('status').notNull().default('ok'),
  uptime90d: jsonb('uptime_90d').notNull().default('[]'),
  sortOrder: integer('sort_order').notNull().default(0),
  // Product/scope-only: which brand skin + which host lands here.
  brand: text('brand'),
  domain: text('domain'),
  launched: boolean('launched').notNull().default(true),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  archivedAt: timestamp('archived_at', { withTimezone: true }),
}, (table) => [
  index('components_parent_idx').on(table.parentId),
  index('components_kind_idx').on(table.kind),
]);

export const incidents = pgTable('incidents', {
  id: text('id').primaryKey(),
  title: text('title').notNull(),
  summary: text('summary').notNull(),
  status: text('status').notNull(),
  severity: text('severity').notNull(),
  affects: text('affects').array().notNull(),
  // Engine ownership: true = opened by the quorum engine, false = human-declared/overridden.
  auto: boolean('auto').notNull().default(false),
  startedAt: timestamp('started_at', { withTimezone: true }).notNull(),
  resolvedAt: timestamp('resolved_at', { withTimezone: true }),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
}, (table) => [
  index('incidents_status_idx').on(table.status),
  index('incidents_started_at_idx').on(table.startedAt),
]);

export const incidentTimeline = pgTable('incident_timeline', {
  id: text('id').primaryKey(),
  incidentId: text('incident_id').references(() => incidents.id, { onDelete: 'cascade' }),
  at: timestamp('at', { withTimezone: true }).notNull(),
  label: text('label').notNull(),
  body: text('body').notNull(),
  // Who wrote this update: 'engine' for templated auto-updates, otherwise an admin email.
  author: text('author').notNull().default('engine'),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
}, (table) => [
  index('incident_timeline_incident_at_idx').on(table.incidentId, table.at),
]);

export const maintenance = pgTable('maintenance', {
  id: text('id').primaryKey(),
  title: text('title').notNull(),
  summary: text('summary').notNull(),
  scheduledStart: timestamp('scheduled_start', { withTimezone: true }).notNull(),
  scheduledEnd: timestamp('scheduled_end', { withTimezone: true }).notNull(),
  affects: text('affects').array().notNull(),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
});

export const subscribers = pgTable('subscribers', {
  id: text('id').primaryKey(),
  email: text('email').notNull().unique(),
  confirmedAt: timestamp('confirmed_at', { withTimezone: true }),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
});

export const adminSessions = pgTable('admin_sessions', {
  token: text('token').primaryKey(),
  email: text('email').notNull(),
  expiresAt: timestamp('expires_at', { withTimezone: true }).notNull(),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
});

export const adminMagicLinks = pgTable('admin_magic_links', {
  token: text('token').primaryKey(),
  email: text('email').notNull(),
  expiresAt: timestamp('expires_at', { withTimezone: true }).notNull(),
  usedAt: timestamp('used_at', { withTimezone: true }),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
});

export const apiTokens = pgTable('api_tokens', {
  id: text('id').primaryKey(),
  name: text('name').notNull(),
  tokenHash: text('token_hash').notNull(),
  scope: text('scope').notNull().default('full'),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  lastUsedAt: timestamp('last_used_at', { withTimezone: true }),
  expiresAt: timestamp('expires_at', { withTimezone: true }),
  revokedAt: timestamp('revoked_at', { withTimezone: true }),
}, (table) => [
  index('api_tokens_hash_active_idx').on(table.tokenHash),
]);

/* ──────────────────────────────────────────────────────────────
 * Source-agnostic core engine
 *
 * A `source` is any independent vantage that reports observations
 * (the app self-report, host metrics, an external probe, or a human
 * override). `observations` is an append-only log; component status is
 * DERIVED from the latest non-expired observation per source via quorum,
 * never stored. `source_target_map` is the ONLY place vendor vocabulary
 * (raw labels) touches the model — it maps a source's raw_label to a
 * component (a `components.id`, a leaf node in the tree).
 * ────────────────────────────────────────────────────────────── */

export const sources = pgTable('sources', {
  id: text('id').primaryKey(),
  name: text('name').notNull(),
  tokenHash: text('token_hash').notNull(),
  // Trust weight; a higher-weight source (e.g. 'manual' override) counts more.
  weight: integer('weight').notNull().default(1),
  // 'push' (POSTs directly), 'probe' (external check), 'heartbeat' (must report or go stale), 'manual' (human override).
  kind: text('kind').notNull().default('push'),
  // First-party trust. A `trusted` source is authoritative ground truth (the
  // app's own self-report). It can DECLARE an incident ON ITS OWN — at reduced
  // confidence (capped to degraded), never invisible — and a second source
  // ESCALATES it to a confirmed/major outage. An untrusted source is a
  // VALIDATOR: alone it only WATCHes (external false-positive guard); it needs
  // quorum (>=2 agree) to declare. Default false — trust is opt-in per source.
  trusted: boolean('trusted').notNull().default(false),
  // Seconds; how long an observation from this source stays valid if it sets no explicit expires_at.
  defaultTtl: integer('default_ttl'),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  revokedAt: timestamp('revoked_at', { withTimezone: true }),
}, (table) => [
  index('sources_token_hash_idx').on(table.tokenHash),
]);

export const observations = pgTable('observations', {
  id: text('id').primaryKey(),
  sourceId: text('source_id').notNull().references(() => sources.id, { onDelete: 'cascade' }),
  // The resolved component this observation is about (a components.id).
  componentId: text('component_id').notNull().references(() => components.id),
  // 'ok' | 'degraded' | 'down'.
  signal: text('signal').notNull(),
  detail: text('detail'),
  observedAt: timestamp('observed_at', { withTimezone: true }).notNull().defaultNow(),
  // Null = never expires; otherwise the dead-man / TTL horizon.
  expiresAt: timestamp('expires_at', { withTimezone: true }),
}, (table) => [
  // Hot path: latest-per-(source,component) lookups for quorum.
  index('observations_component_observed_idx').on(table.componentId, table.observedAt),
  index('observations_source_component_observed_idx').on(table.sourceId, table.componentId, table.observedAt),
]);

export const sourceTargetMap = pgTable('source_target_map', {
  id: text('id').primaryKey(),
  sourceId: text('source_id').notNull().references(() => sources.id, { onDelete: 'cascade' }),
  // The vendor's raw label for the target (e.g. "payments", "stripe-svc").
  rawLabel: text('raw_label').notNull(),
  // The component (components.id) this raw label resolves to.
  componentId: text('component_id').notNull().references(() => components.id),
}, (table) => [
  index('source_target_map_lookup_idx').on(table.sourceId, table.rawLabel),
]);
