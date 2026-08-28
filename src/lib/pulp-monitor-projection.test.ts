import { describe, expect, it } from 'vitest';
import {
  buildPulpComponentCrumbs,
  buildPulpComponentView,
  buildPulpIncident,
  buildPulpIncidentHistory,
  buildPulpStatusJSON,
  buildPulpSummaryTree,
} from './pulp-monitor-projection';
import type { MonitorProjection } from './pulp-bridge';

function projection(): MonitorProjection {
  const evaluation = (id: string, status: 'operational' | 'degraded' | 'outage') => ({
    component_id: id,
    status,
    state: status === 'operational' ? 'operational' : 'declared',
    reads: [],
    non_ok_count: status === 'operational' ? 0 : 1,
    non_ok_weight: status === 'operational' ? 0 : 1,
    trusted_non_ok_count: 0,
    stale_count: 0,
    reduced_coverage: false,
    has_live_reads: true,
  });
  const component = (
    id: string,
    name: string,
    kind: string,
    parentId: string | undefined,
    status: 'operational' | 'degraded' | 'outage',
    critical = false,
    sortOrder = 0,
  ) => ({
    component: {
      id,
      parent_id: parentId,
      name,
      kind,
      sort_order: sortOrder,
      fallback_status: 'operational' as const,
      critical,
      archived: false,
    },
    own_evaluation: evaluation(id, status),
    evaluation: evaluation(id, status),
  });
  return {
    version: 'monitor.v1',
    revision: 10,
    components: [
      component('example', 'Example', 'organization', undefined, 'operational'),
      component('product', 'Product', 'product', 'example', 'operational'),
      component('api', 'API', 'service', 'product', 'outage', false, 20),
      component('database', 'Database', 'critical', 'product', 'operational', true, 10),
    ],
    sources: [],
    mappings: [],
    incidents: [{
      id: 'incident-1',
      title: 'API issue',
      summary: 'Investigating',
      status: 'investigating',
      severity: 'moderate',
      affects: ['api'],
      auto: true,
      started_at_unix: 1_800_000_000,
    }],
    incident_updates: [],
    maintenance: [{
      id: 'maintenance-1',
      title: 'Database work',
      summary: 'Routine',
      kind: 'scheduled',
      scheduled_start_unix: 1_800_000_100,
      scheduled_end_unix: 1_800_000_200,
      affects: ['database'],
      cancelled: false,
    }],
  };
}

describe('Pulp monitor public projection adapter', () => {
  it('preserves the legacy partial-outage floor and summary shape', () => {
    const tree = buildPulpSummaryTree(projection(), null);
    expect(tree.id).toBe('example');
    expect(tree.status).toBe('degraded');
    expect(tree.issueCount).toBe(1);
    expect(tree.children[0].children.map((child) => child.id)).toEqual(['database', 'api']);
    expect(tree.children[0].children[1].status).toBe('outage');
    expect(tree.children[0].children[1].incidents[0]).toMatchObject({
      id: 'incident-1',
      started: '2027-01-15T08:00:00.000Z',
    });
  });

  it('builds the exact status JSON fields without exposing owner internals', () => {
    const body = buildPulpStatusJSON(projection(), null, new Date(1_800_000_050 * 1000));
    expect(body).toEqual({
      status: 'degraded',
      state: 'degraded',
      services: [
        { id: 'database', name: 'Database', product: 'product', status: 'operational' },
        { id: 'api', name: 'API', product: 'product', status: 'outage' },
      ],
      activeIncidents: [{
        id: 'incident-1',
        title: 'API issue',
        severity: 'moderate',
        status: 'investigating',
        started: '2027-01-15T08:00:00.000Z',
      }],
      scheduledMaintenance: [{
        id: 'maintenance-1',
        title: 'Database work',
        scheduledStart: '2027-01-15T08:01:40.000Z',
        scheduledEnd: '2027-01-15T08:03:20.000Z',
      }],
    });
  });

  it('fails closed on dangling owner references', () => {
    const value = projection();
    value.incidents[0].affects = ['missing'];
    expect(() => buildPulpSummaryTree(value, null)).toThrow(/invalid component/);
  });

  it('fails closed when the configured scope root is absent', () => {
    const value = projection();
    value.components = value.components.filter((item) => item.component.id !== 'example');
    expect(() => buildPulpSummaryTree(value, null)).toThrow();
  });

  it('maps scoped incident history, pagination, and timelines without a database read', () => {
    const value = projection();
    value.incidents.push({
      id: 'incident-2',
      title: 'Database issue',
      summary: 'Resolved',
      status: 'resolved',
      severity: 'major',
      affects: ['database'],
      auto: false,
      started_at_unix: 1_799_990_000,
      resolved_at_unix: 1_799_990_100,
    });
    value.incident_updates = [
      {
        id: 'update-1',
        incident_id: 'incident-1',
        at_unix: 1_800_000_010,
        label: 'Investigating',
        body: 'First update',
        author: 'operator',
      },
      {
        id: 'update-2',
        incident_id: 'incident-1',
        at_unix: 1_800_000_020,
        label: 'Monitoring',
        body: 'Latest update',
        author: 'operator',
      },
    ];

    const first = buildPulpIncidentHistory(value, null, 1, 0);
    expect(first.total).toBe(2);
    expect(first.incidents).toHaveLength(1);
    expect(first.incidents[0]).toMatchObject({
      id: 'incident-1',
      product: 'product',
      timeline: [
        { status: 'monitoring', body: 'Latest update' },
        { status: 'investigating', body: 'First update' },
      ],
    });
    expect(buildPulpIncidentHistory(value, null, 1, 1).incidents[0].id).toBe('incident-2');
  });

  it('builds incident detail and scope-relative component crumbs', () => {
    const value = projection();
    value.incident_updates.push({
      id: 'update-1',
      incident_id: 'incident-1',
      at_unix: 1_800_000_010,
      label: 'Identified',
      body: 'Cause found',
      author: 'operator',
    });

    expect(buildPulpIncident(value, 'incident-1')).toMatchObject({
      id: 'incident-1',
      product: 'product',
      timeline: [{ status: 'identified', body: 'Cause found' }],
    });
    expect(buildPulpComponentCrumbs(value, null, 'database')).toEqual({
      crumbs: [
        { label: 'Example', href: '/' },
        { label: 'Product', href: '/product' },
        { label: 'Database', href: '/product/database' },
      ],
      affectedPath: ['Example', 'Product', 'Database'],
    });
  });

  it('builds the legacy-compatible component page model from owner state', () => {
    const value = projection();
    const yesterday = '2027-01-14';
    for (const item of value.components) {
      item.component.uptime_90d = [{ date: yesterday, status: 'ok' }];
    }

    const view = buildPulpComponentView(
      value,
      null,
      ['product'],
      new Date(1_800_000_150 * 1000),
    );

    expect(view).not.toBeNull();
    expect(view).toMatchObject({
      status: 'degraded',
      state: 'degraded',
      nodeName: 'Product',
      level: 'product',
      issueCount: 1,
      maintCount: 1,
      affectedChildNames: ['API'],
      uptimePct: 50,
    });
    expect(view!.children.map((child) => child.id)).toEqual(['database', 'api']);
    expect(view!.children[1]).toMatchObject({
      status: 'outage',
      issueCount: 1,
      uptimePct: 50,
    });
    expect(view!.uptime.slice(-2)).toEqual(['ok', 'deg']);
    expect(view!.dayIncidents[0]).toMatchObject({
      id: 'incident-1',
      affectedName: 'API',
      affects: ['api'],
    });
    expect(view!.maintenance[0]).toMatchObject({
      id: 'maintenance-1',
      active: true,
    });
  });

  it('fails closed on malformed resolved history and timeline references', () => {
    const danglingIncident = projection();
    danglingIncident.incidents[0].status = 'resolved';
    danglingIncident.incidents[0].resolved_at_unix = 1_800_000_100;
    danglingIncident.incidents[0].affects = ['missing'];
    expect(() => buildPulpIncidentHistory(danglingIncident, null)).toThrow(/invalid component/);

    const danglingUpdate = projection();
    danglingUpdate.incident_updates.push({
      id: 'update-bad',
      incident_id: 'missing',
      at_unix: 1_800_000_010,
      label: 'Investigating',
      body: 'Bad reference',
      author: 'operator',
    });
    expect(() => buildPulpComponentView(danglingUpdate, null, [])).toThrow(/invalid incident/);
  });
});
