// Canonical status schema for statuskit v0.1.
//
// Intentionally tiny — we want every consumer's upstream to map down to
// this shape. Subsystems form a tree; v0.1 only renders one level deep,
// but `children` is in the shape so reduce.ts and the future renderer
// can recurse without a schema change.

export type Health = 'operational' | 'degraded' | 'major' | 'maintenance';

export interface Subsystem {
  name: string;
  status: Health;
  message?: string;
  /** Optional numeric meter (0-100), e.g. capacity %. */
  meter?: { label: string; value: number; max: number };
  children?: Subsystem[];
}

export interface CanonicalStatus {
  overall: Health;
  subsystems: Subsystem[];
  /** ISO timestamp from the upstream probe. */
  updatedAt: string;
  /** Optional human banner above the tree (e.g. "Capacity full, queue 8"). */
  banner?: { tone: Health; text: string };
}

export interface Incident {
  id: string;
  title: string;
  /** ISO timestamp. Newest first when sorted. */
  startedAt: string;
  /** ISO timestamp; absent = ongoing. */
  resolvedAt?: string;
  severity: Exclude<Health, 'operational'>;
  /** Plaintext or simple HTML. v0.1 trusts the committed file. */
  body: string;
}

// ── lightweight validators (zod-lite, no dep) ────────────────────────────

const HEALTHS: ReadonlySet<Health> = new Set([
  'operational',
  'degraded',
  'major',
  'maintenance',
]);

export function isHealth(v: unknown): v is Health {
  return typeof v === 'string' && HEALTHS.has(v as Health);
}

export function assertCanonicalStatus(v: unknown): asserts v is CanonicalStatus {
  if (!v || typeof v !== 'object') throw new Error('status: not an object');
  const o = v as Record<string, unknown>;
  if (!isHealth(o.overall)) throw new Error('status.overall: invalid');
  if (!Array.isArray(o.subsystems)) throw new Error('status.subsystems: not array');
  if (typeof o.updatedAt !== 'string') throw new Error('status.updatedAt: missing');
  for (const s of o.subsystems) assertSubsystem(s);
}

function assertSubsystem(v: unknown): asserts v is Subsystem {
  if (!v || typeof v !== 'object') throw new Error('subsystem: not an object');
  const o = v as Record<string, unknown>;
  if (typeof o.name !== 'string') throw new Error('subsystem.name: missing');
  if (!isHealth(o.status)) throw new Error('subsystem.status: invalid');
  if (o.children !== undefined) {
    if (!Array.isArray(o.children)) throw new Error('subsystem.children: not array');
    for (const c of o.children) assertSubsystem(c);
  }
}

export function assertIncident(v: unknown): asserts v is Incident {
  if (!v || typeof v !== 'object') throw new Error('incident: not an object');
  const o = v as Record<string, unknown>;
  if (typeof o.id !== 'string') throw new Error('incident.id: missing');
  if (typeof o.title !== 'string') throw new Error('incident.title: missing');
  if (typeof o.startedAt !== 'string') throw new Error('incident.startedAt: missing');
  if (typeof o.body !== 'string') throw new Error('incident.body: missing');
  if (!isHealth(o.severity) || o.severity === 'operational') {
    throw new Error('incident.severity: must be degraded|major|maintenance');
  }
}
