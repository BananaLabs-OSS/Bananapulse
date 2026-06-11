import { describe, it, expect, vi, beforeEach } from 'vitest';

// The quorum engine reads the latest observation-per-source via a single raw
// `db.execute(sql\`...\`)`. We mock @/db so that call returns an injectable row
// set — letting us drive evaluateComponent's PURE decision precedence with no
// Postgres. (evaluateComponent performs no writes; insert/update are stubbed.)
const execRows = { current: [] as any[] };

vi.mock('@/db', () => ({
  db: {
    execute: vi.fn(async () => execRows.current),
    select: vi.fn(() => ({ from: () => ({ where: async () => [] }) })),
    insert: vi.fn(() => ({ values: async () => undefined })),
    update: vi.fn(() => ({ set: () => ({ where: async () => undefined }) })),
  },
}));

import { evaluateComponent, evaluationToStatus, MANUAL_OK_GRACE_SECONDS, sameObservation, resolveExpiry, refreshExpiry, confidenceToStatus } from './quorum';

const NOW = new Date('2026-06-03T12:00:00Z');

// Build a DISTINCT-ON-shaped row as evaluateComponent expects from db.execute.
function row(opts: {
  sourceId: string;
  kind?: 'monitor' | 'manual';
  signal: 'ok' | 'degraded' | 'down';
  weight?: number;
  trusted?: boolean; // first-party (default false = external validator)
  revoked?: boolean;
  expiresInSec?: number | null; // null/undefined => never expires
}) {
  const expires =
    opts.expiresInSec === undefined || opts.expiresInSec === null
      ? null
      : new Date(NOW.getTime() + opts.expiresInSec * 1000);
  return {
    source_id: opts.sourceId,
    name: opts.sourceId,
    weight: opts.weight ?? 1,
    kind: opts.kind ?? 'monitor',
    trusted: opts.trusted ?? false,
    revoked_at: opts.revoked ? NOW : null,
    signal: opts.signal,
    detail: null,
    observed_at: NOW,
    expires_at: expires,
  };
}

async function evalWith(rows: any[]) {
  execRows.current = rows;
  return evaluateComponent('c1', NOW);
}

beforeEach(() => {
  execRows.current = [];
});

describe('quorum evaluateComponent — decision precedence (audit R5)', () => {
  it('zero non-ok reads => operational/ok', async () => {
    const ev = await evalWith([row({ sourceId: 'm1', signal: 'ok' }), row({ sourceId: 'm2', signal: 'ok' })]);
    expect(ev.state).toBe('ok');
    expect(ev.level).toBeNull();
    expect(evaluationToStatus(ev)).toBe('operational');
  });

  it('exactly one UNTRUSTED monitor non-ok => watch (validator, never declares/pages alone)', async () => {
    const ev = await evalWith([row({ sourceId: 'm1', signal: 'down' }), row({ sourceId: 'm2', signal: 'ok' })]);
    expect(ev.state).toBe('watch');
    expect(ev.level).toBeNull();
    expect(evaluationToStatus(ev)).toBe('operational'); // watch reads operational publicly
  });

  it('>=2 monitors non-ok => declared (threshold pin)', async () => {
    const ev = await evalWith([row({ sourceId: 'm1', signal: 'degraded' }), row({ sourceId: 'm2', signal: 'degraded' })]);
    expect(ev.state).toBe('declared');
    expect(ev.level).toBe('degraded');
    expect(evaluationToStatus(ev)).toBe('degraded');
  });

  it('>=2 monitors with any "down" => declared MAJOR (worst-agreeing level)', async () => {
    const ev = await evalWith([row({ sourceId: 'm1', signal: 'down' }), row({ sourceId: 'm2', signal: 'degraded' })]);
    expect(ev.state).toBe('declared');
    expect(ev.level).toBe('major');
    expect(evaluationToStatus(ev)).toBe('outage');
  });

  // --- THE R5 ENGINE FIX: manual 'ok' is SUBORDINATE to >=2-monitor corroboration ---

  it('REGRESSION (R5 CRITICAL): a live manual "ok" CANNOT suppress a >=2-monitor outage', async () => {
    // Two independent monitors report down; an admin "all clear" (manual ok) is
    // also live. Pre-fix, the manual ok out-voted everything (page green forever
    // via re-POSTed resolve). Post-fix, monitor corroboration wins: still declared.
    const ev = await evalWith([
      row({ sourceId: 'm1', signal: 'down' }),
      row({ sourceId: 'm2', signal: 'down' }),
      row({ sourceId: 'manual', kind: 'manual', signal: 'ok', weight: 100, expiresInSec: MANUAL_OK_GRACE_SECONDS }),
    ]);
    expect(ev.state).toBe('declared');
    expect(ev.level).toBe('major');
    expect(evaluationToStatus(ev)).toBe('outage');
  });

  it('REGRESSION: a high-weight manual "ok" cannot out-vote monitors (gating is kind!=manual, not weight)', async () => {
    const ev = await evalWith([
      row({ sourceId: 'm1', signal: 'degraded' }),
      row({ sourceId: 'm2', signal: 'degraded' }),
      row({ sourceId: 'manual', kind: 'manual', signal: 'ok', weight: 9999 }),
    ]);
    expect(ev.state).toBe('declared');
    expect(ev.level).toBe('degraded');
  });

  it('manual "ok" DOES clear a single-monitor (sub-corroboration) situation', async () => {
    // Only one monitor disagrees => human "all clear" is allowed to carry.
    const ev = await evalWith([
      row({ sourceId: 'm1', signal: 'down' }),
      row({ sourceId: 'manual', kind: 'manual', signal: 'ok', expiresInSec: MANUAL_OK_GRACE_SECONDS }),
    ]);
    expect(ev.state).toBe('ok');
    expect(evaluationToStatus(ev)).toBe('operational');
  });

  it('a live manual NON-ok DECLARES on its own (human escalation authoritative)', async () => {
    const ev = await evalWith([row({ sourceId: 'manual', kind: 'manual', signal: 'down' })]);
    expect(ev.state).toBe('declared');
    expect(ev.level).toBe('major');
  });

  it('manual "degraded" declares at degraded level', async () => {
    const ev = await evalWith([row({ sourceId: 'manual', kind: 'manual', signal: 'degraded' })]);
    expect(ev.state).toBe('declared');
    expect(ev.level).toBe('degraded');
  });

  it('an EXPIRED manual "ok" drops out (stale) and the engine falls back to monitor quorum', async () => {
    // Manual ok expired (grace elapsed). One live monitor down remains => watch,
    // not a masked "ok". A stale resolve can never permanently mask an outage.
    const ev = await evalWith([
      row({ sourceId: 'm1', signal: 'down' }),
      row({ sourceId: 'manual', kind: 'manual', signal: 'ok', expiresInSec: -10 }),
    ]);
    expect(ev.state).toBe('watch');
    expect(ev.reducedCoverage).toBe(true); // the expired manual read counts as stale coverage loss
  });

  it('revoked sources are ignored entirely (cannot contribute to quorum)', async () => {
    const ev = await evalWith([
      row({ sourceId: 'm1', signal: 'down', revoked: true }),
      row({ sourceId: 'm2', signal: 'down', revoked: true }),
    ]);
    expect(ev.state).toBe('ok');
    expect(ev.totalSources).toBe(0);
  });

  it('dead-man: all reads stale => no live reads, reducedCoverage set (hold, do not auto-resolve)', async () => {
    const ev = await evalWith([
      row({ sourceId: 'm1', signal: 'down', expiresInSec: -100 }),
      row({ sourceId: 'm2', signal: 'down', expiresInSec: -100 }),
    ]);
    expect(ev.hasLiveReads).toBe(false);
    expect(ev.reducedCoverage).toBe(true);
    // state derives ok from zero LIVE non-ok reads — reconcileIncident's
    // hasLiveReads guard is what prevents auto-resolve during blackout.
    expect(ev.state).toBe('ok');
  });
});

describe('quorum trusted-source ladder — single first-party declares, corroboration escalates', () => {
  it('DECOUPLE: a lone TRUSTED "down" declares at TRUE severity (major), confidence 1 — not capped to degraded', async () => {
    // The Resend case: only Evolution (trusted) probes it. It declares — visible —
    // at its REAL severity (major); the low confidence of one vantage is carried
    // in the incident STATUS (investigating), not by softening severity to degraded.
    const ev = await evalWith([row({ sourceId: 'evolution', trusted: true, signal: 'down' })]);
    expect(ev.state).toBe('declared');
    expect(ev.level).toBe('major');
    expect(ev.confidence).toBe(1);
    expect(ev.trustedNonOkCount).toBe(1);
    expect(evaluationToStatus(ev)).toBe('outage');
  });

  it('a lone TRUSTED "degraded" declares at degraded', async () => {
    const ev = await evalWith([row({ sourceId: 'evolution', trusted: true, signal: 'degraded' })]);
    expect(ev.state).toBe('declared');
    expect(ev.level).toBe('degraded');
  });

  it('ESCALATION: trusted "down" + an external validator agreeing => MAJOR, confidence 2 (status -> identified)', async () => {
    const ev = await evalWith([
      row({ sourceId: 'evolution', trusted: true, signal: 'down' }),
      row({ sourceId: 'uptimerobot', trusted: false, signal: 'down' }),
    ]);
    expect(ev.state).toBe('declared');
    expect(ev.level).toBe('major');    // severity already major from the lone trusted; corroboration doesn't change it
    expect(ev.confidence).toBe(2);     // the 2nd vantage raises CONFIDENCE, not severity
    expect(evaluationToStatus(ev)).toBe('outage');
  });

  it('a lone UNTRUSTED validator "down" stays WATCH (cannot declare without corroboration)', async () => {
    const ev = await evalWith([row({ sourceId: 'uptimerobot', trusted: false, signal: 'down' })]);
    expect(ev.state).toBe('watch');
    expect(ev.trustedNonOkCount).toBe(0);
  });

  it('a live manual "all clear" still outranks a single trusted vantage (human authority over one source)', async () => {
    const ev = await evalWith([
      row({ sourceId: 'evolution', trusted: true, signal: 'down' }),
      row({ sourceId: 'manual', kind: 'manual', signal: 'ok', expiresInSec: MANUAL_OK_GRACE_SECONDS }),
    ]);
    expect(ev.state).toBe('ok');
    expect(evaluationToStatus(ev)).toBe('operational');
  });

  it('a stale trusted "down" drops out (dead-man) => no phantom declare', async () => {
    const ev = await evalWith([row({ sourceId: 'evolution', trusted: true, signal: 'down', expiresInSec: -10 })]);
    expect(ev.state).toBe('ok');
    expect(ev.trustedNonOkCount).toBe(0);
    expect(ev.reducedCoverage).toBe(true);
  });
});

describe('evaluationToStatus mapping', () => {
  it('declared+major => outage, declared+degraded => degraded, else operational', () => {
    expect(evaluationToStatus({ state: 'declared', level: 'major' } as any)).toBe('outage');
    expect(evaluationToStatus({ state: 'declared', level: 'degraded' } as any)).toBe('degraded');
    expect(evaluationToStatus({ state: 'watch', level: null } as any)).toBe('operational');
    expect(evaluationToStatus({ state: 'ok', level: null } as any)).toBe('operational');
  });
});

describe('sameObservation — idempotency key (signal + observed instant)', () => {
  const t = new Date('2026-06-03T12:00:00Z');
  it('null/undefined latest => never a duplicate', () => {
    expect(sameObservation(null, { signal: 'down', observedAt: t })).toBe(false);
    expect(sameObservation(undefined, { signal: 'down', observedAt: t })).toBe(false);
  });
  it('same signal AND same observed instant => duplicate (an at-least-once retry)', () => {
    expect(sameObservation({ signal: 'down', observedAt: t }, { signal: 'down', observedAt: new Date(t) })).toBe(true);
  });
  it('same signal, different observed instant => NOT a duplicate (legit re-assertion)', () => {
    const later = new Date(t.getTime() + 60_000);
    expect(sameObservation({ signal: 'down', observedAt: t }, { signal: 'down', observedAt: later })).toBe(false);
  });
  it('different signal, same instant => NOT a duplicate (real state change)', () => {
    expect(sameObservation({ signal: 'ok', observedAt: t }, { signal: 'down', observedAt: new Date(t) })).toBe(false);
  });
});

describe('resolveExpiry — dead-man horizon from OBSERVED time', () => {
  const observed = new Date('2026-06-03T12:00:00Z');
  it('explicit expires_at wins over TTL', () => {
    const explicit = new Date('2026-06-03T13:00:00Z');
    expect(resolveExpiry(observed, explicit, 600)?.getTime()).toBe(explicit.getTime());
  });
  it('TTL is measured from observedAt, not now', () => {
    expect(resolveExpiry(observed, null, 600)?.getTime()).toBe(observed.getTime() + 600_000);
  });
  it('no explicit + no/zero TTL => null (never expires)', () => {
    expect(resolveExpiry(observed, null, null)).toBeNull();
    expect(resolveExpiry(observed, undefined, 0)).toBeNull();
  });
});

describe('refreshExpiry — re-assertion slides the dead-man horizon from receipt time', () => {
  const now = new Date('2026-06-03T12:00:00Z');
  it('extends an earlier horizon forward (repeat keeps a still-firing signal live)', () => {
    const existing = new Date('2026-06-03T12:05:00Z');
    expect(refreshExpiry(existing, now, null, 600)?.getTime()).toBe(now.getTime() + 600_000);
  });
  it('never shortens a later horizon', () => {
    const existing = new Date('2026-06-03T13:00:00Z');
    expect(refreshExpiry(existing, now, null, 600)).toBeNull();
  });
  it('replaces a NULL (never-expires) horizon once the source declares a TTL', () => {
    expect(refreshExpiry(null, now, null, 600)?.getTime()).toBe(now.getTime() + 600_000);
    expect(refreshExpiry(undefined, now, null, 600)?.getTime()).toBe(now.getTime() + 600_000);
  });
  it('re-assertion without a TTL leaves the row untouched', () => {
    expect(refreshExpiry(null, now, null, null)).toBeNull();
    expect(refreshExpiry(new Date('2026-06-03T12:01:00Z'), now, undefined, 0)).toBeNull();
  });
  it('explicit expires_at wins over TTL, still only extends forward', () => {
    const explicit = new Date('2026-06-03T14:00:00Z');
    expect(refreshExpiry(null, now, explicit, 600)?.getTime()).toBe(explicit.getTime());
    expect(refreshExpiry(new Date('2026-06-03T15:00:00Z'), now, explicit, 600)).toBeNull();
  });
});

describe('confidenceToStatus — vantages set incident status, never severity', () => {
  it('1 vantage => investigating, >=2 => identified', () => {
    expect(confidenceToStatus(0)).toBe('investigating');
    expect(confidenceToStatus(1)).toBe('investigating');
    expect(confidenceToStatus(2)).toBe('identified');
    expect(confidenceToStatus(5)).toBe('identified');
  });
});
