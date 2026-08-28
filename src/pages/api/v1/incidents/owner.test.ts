import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/lib/pulp-bridge', () => ({
  callPulpEvent: vi.fn(),
  PULP_EVENTS: {
    monitorQuery: 'monitor-query',
    monitorAdminCommand: 'monitor-command',
    incidentPublish: 'incident-publish',
  },
  PulpBridgeError: class extends Error {
    status = 503;
  },
  pulpOwnerRequestID: vi.fn((scope: string, identity: string) => `${scope}:${identity}`),
}));

import { newOwnerCommand } from './owner';

describe('newOwnerCommand', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it.each([
    ['open', 'open_incident'],
    ['edit', 'edit_incident'],
    ['update', 'update_incident'],
    ['resolve', 'resolve_incident'],
    ['delete', 'delete_incident'],
  ])('maps %s to the canonical monitor command %s', (operation, canonicalKind) => {
    vi.useFakeTimers();
    vi.setSystemTime('2026-07-26T12:00:00.000Z');

    expect(newOwnerCommand(operation, 'incident-1', { incident_id: 'incident-1' })).toEqual({
      version: 'monitor.v1',
      id: `incident-${operation}:incident-1`,
      kind: canonicalKind,
      at_unix: 1_785_067_200,
      incident_id: 'incident-1',
    });
  });

  it('preserves an already canonical or future monitor command', () => {
    const command = newOwnerCommand('acknowledge_incident', 'incident-1');

    expect(command.kind).toBe('acknowledge_incident');
  });
});
