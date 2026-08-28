import { afterEach, describe, expect, it } from 'vitest';
import {
  pulpBridgeConfigured,
  pulpMonitorProjectionConfigured,
  pulpOwnerRouteFamilyConfigured,
  pulpSubscriberLifecycleConfigured,
} from './pulp-bridge';

const keys = [
  'PULP_BRIDGE_URL',
  'PULP_MONITOR_OWNER_ENABLED',
  'PULP_SUBSCRIBERS_OWNER_ENABLED',
  'PULP_SUBSCRIBER_TOKEN_SECRET',
  'PULP_INCIDENTS_OWNER_ENABLED',
] as const;
const original = Object.fromEntries(keys.map((key) => [key, process.env[key]]));

afterEach(() => {
  for (const key of keys) {
    const value = original[key];
    if (value === undefined) delete process.env[key];
    else process.env[key] = value;
  }
});

describe('Pulp route-family cutover gates', () => {
  it('never enables a family from a flag alone without a bridge', () => {
    delete process.env.PULP_BRIDGE_URL;
    process.env.PULP_INCIDENTS_OWNER_ENABLED = 'true';
    expect(pulpBridgeConfigured()).toBe(false);
    expect(pulpOwnerRouteFamilyConfigured('incidents')).toBe(false);
  });

  it('enables only the explicitly selected family', () => {
    process.env.PULP_BRIDGE_URL = 'http://127.0.0.1:8788';
    process.env.PULP_INCIDENTS_OWNER_ENABLED = 'true';
    expect(pulpOwnerRouteFamilyConfigured('incidents')).toBe(true);
    expect(pulpOwnerRouteFamilyConfigured('maintenance')).toBe(false);
  });

  it('requires the additional subscriber token secret for public lifecycle cutover', () => {
    process.env.PULP_BRIDGE_URL = 'http://127.0.0.1:8788';
    process.env.PULP_SUBSCRIBERS_OWNER_ENABLED = 'true';
    delete process.env.PULP_SUBSCRIBER_TOKEN_SECRET;
    expect(pulpSubscriberLifecycleConfigured()).toBe(false);
    process.env.PULP_SUBSCRIBER_TOKEN_SECRET = 'test-only-secret';
    expect(pulpSubscriberLifecycleConfigured()).toBe(true);
  });

  it('keeps public monitor reads independently gated', () => {
    process.env.PULP_BRIDGE_URL = 'http://127.0.0.1:8788';
    delete process.env.PULP_MONITOR_OWNER_ENABLED;
    expect(pulpMonitorProjectionConfigured()).toBe(false);
    process.env.PULP_MONITOR_OWNER_ENABLED = 'true';
    expect(pulpMonitorProjectionConfigured()).toBe(true);
  });
});

