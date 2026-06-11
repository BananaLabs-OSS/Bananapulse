import { describe, it, expect } from 'vitest';
import { grafanaObservedAt } from './grafana';

describe('grafanaObservedAt — firing uses startsAt, resolved uses endsAt', () => {
  const startsAt = '2026-06-09T15:26:30Z';
  const endsAt = '2026-06-10T08:00:00Z';

  it('firing => startsAt', () => {
    expect(grafanaObservedAt({ status: 'firing', startsAt, endsAt })?.toISOString())
      .toBe(new Date(startsAt).toISOString());
  });

  it('resolved => endsAt, NOT the pinned startsAt (would tie with the down read)', () => {
    const got = grafanaObservedAt({ status: 'resolved', startsAt, endsAt });
    expect(got?.toISOString()).toBe(new Date(endsAt).toISOString());
    expect(got?.getTime()).not.toBe(new Date(startsAt).getTime());
  });

  it("Alertmanager's zero-value endsAt (0001-01-01) is treated as absent", () => {
    expect(grafanaObservedAt({ status: 'resolved', startsAt, endsAt: '0001-01-01T00:00:00Z' })).toBeUndefined();
  });

  it('missing/invalid timestamps => undefined (caller falls back to ingest time)', () => {
    expect(grafanaObservedAt({ status: 'firing' })).toBeUndefined();
    expect(grafanaObservedAt({ status: 'firing', startsAt: 'not-a-date' })).toBeUndefined();
    expect(grafanaObservedAt({ status: 'resolved', startsAt })).toBeUndefined();
    expect(grafanaObservedAt({ status: 'firing', startsAt: 42 as unknown as string })).toBeUndefined();
  });
});
