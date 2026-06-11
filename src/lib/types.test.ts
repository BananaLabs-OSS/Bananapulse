import { describe, it, expect } from 'vitest';
import { worstStatus, statusToState, type ServiceStatus, type Service } from './types';

// worstStatus is the worst-of bubbling primitive the component tree rolls up
// through (effective() in components.ts uses the same priority order). Pinning
// it guards the "container shows worst-of-subtree" invariant.
function svc(status: ServiceStatus): Service {
  return { id: status, name: status, product: 'p', status, uptime90d: [] };
}

describe('worstStatus (worst-of-subtree bubbling)', () => {
  it('outage dominates everything', () => {
    expect(worstStatus([svc('operational'), svc('maintenance'), svc('degraded'), svc('outage')])).toBe('outage');
  });
  it('degraded beats maintenance and operational', () => {
    expect(worstStatus([svc('operational'), svc('maintenance'), svc('degraded')])).toBe('degraded');
  });
  it('maintenance beats operational', () => {
    expect(worstStatus([svc('operational'), svc('maintenance')])).toBe('maintenance');
  });
  it('all-operational stays operational', () => {
    expect(worstStatus([svc('operational'), svc('operational')])).toBe('operational');
  });
  it('empty set is operational', () => {
    expect(worstStatus([])).toBe('operational');
  });
  it('priority order is outage > degraded > maintenance > operational', () => {
    expect(worstStatus([svc('degraded'), svc('outage')])).toBe('outage');
    expect(worstStatus([svc('maintenance'), svc('outage')])).toBe('outage');
    expect(worstStatus([svc('maintenance'), svc('degraded')])).toBe('degraded');
  });
});

describe('statusToState', () => {
  it('maps each ServiceStatus to its state class', () => {
    expect(statusToState('operational')).toBe('working');
    expect(statusToState('degraded')).toBe('degraded');
    expect(statusToState('outage')).toBe('outage');
    expect(statusToState('maintenance')).toBe('queued');
  });
});
