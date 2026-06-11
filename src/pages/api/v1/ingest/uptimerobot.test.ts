import { describe, it, expect, vi } from 'vitest';

// The route module imports @/db (and libs that import it). Mock it so importing
// the pure mapping helpers never constructs a real Postgres client.
vi.mock('@/db', () => ({
  db: {
    select: vi.fn(() => ({ from: () => ({ where: async () => [] }) })),
    insert: vi.fn(() => ({ values: async () => undefined })),
    update: vi.fn(() => ({ set: () => ({ where: async () => undefined }) })),
    execute: vi.fn(async () => []),
  },
}));

import { uptimeAlertTypeToSignal, parseUptimeObservedAt, uptimeRawTarget } from './uptimerobot';

describe('uptimeAlertTypeToSignal', () => {
  it('1 => down, 2 => ok, 3 => ssl (string or number form)', () => {
    expect(uptimeAlertTypeToSignal('1')).toBe('down');
    expect(uptimeAlertTypeToSignal(1)).toBe('down');
    expect(uptimeAlertTypeToSignal('2')).toBe('ok');
    expect(uptimeAlertTypeToSignal(2)).toBe('ok');
    expect(uptimeAlertTypeToSignal('3')).toBe('ssl');
  });
  it('unknown/missing => null', () => {
    expect(uptimeAlertTypeToSignal('9')).toBeNull();
    expect(uptimeAlertTypeToSignal(undefined)).toBeNull();
    expect(uptimeAlertTypeToSignal('')).toBeNull();
  });
});

describe('parseUptimeObservedAt (unix seconds -> Date)', () => {
  it('valid unix seconds => Date in ms', () => {
    expect(parseUptimeObservedAt('1700000000')?.getTime()).toBe(1700000000 * 1000);
    expect(parseUptimeObservedAt(1700000000)?.getTime()).toBe(1700000000 * 1000);
  });
  it('absent/invalid => null', () => {
    expect(parseUptimeObservedAt(undefined)).toBeNull();
    expect(parseUptimeObservedAt('')).toBeNull();
    expect(parseUptimeObservedAt('abc')).toBeNull();
    expect(parseUptimeObservedAt(0)).toBeNull();
  });
});

describe('uptimeRawTarget (monitorID preferred, friendly-name fallback)', () => {
  it('prefers monitorID', () => {
    expect(uptimeRawTarget({ monitorID: 12345, monitorFriendlyName: 'API' })).toBe('12345');
  });
  it('falls back to friendly name when no id', () => {
    expect(uptimeRawTarget({ monitorFriendlyName: 'api-health' })).toBe('api-health');
    expect(uptimeRawTarget({ monitorID: '', monitorFriendlyName: 'x' })).toBe('x');
  });
  it('null when neither present', () => {
    expect(uptimeRawTarget({})).toBeNull();
    expect(uptimeRawTarget({ monitorID: '   ' })).toBeNull();
  });
});
