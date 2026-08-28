import { rootComponentId } from '@/pulse.config';
import type {
  MonitorComponent,
  MonitorEvaluation,
  MonitorProjection,
  MonitorStatus,
} from './pulp-bridge';
import {
  statusToState,
  type Incident,
  type IncidentSeverity,
  type IncidentStatus,
  type ServiceStatus,
  type TimelineEntry,
} from './types';
import type { SummaryNode } from './components';
import type {
  CrumbItem,
  DayIncident,
  MaintWindow,
  ScopeView,
  ViewChild,
} from './view';

type ProjectionNode = {
  component: MonitorComponent;
  own: MonitorEvaluation;
  children: ProjectionNode[];
};

const STATUS_RANK: Record<MonitorStatus, number> = {
  operational: 0,
  degraded: 1,
  outage: 2,
};

function isStatus(value: unknown): value is MonitorStatus {
  return value === 'operational' || value === 'degraded' || value === 'outage';
}

function isIncidentStatus(value: unknown): value is IncidentStatus {
  return value === 'investigating' ||
    value === 'identified' ||
    value === 'monitoring' ||
    value === 'resolved';
}

function isIncidentSeverity(value: unknown): value is IncidentSeverity {
  return value === 'minor' || value === 'moderate' || value === 'major';
}

function iso(unix: number): string {
  if (!Number.isFinite(unix) || unix <= 0) throw new Error('invalid owner timestamp');
  return new Date(unix * 1000).toISOString();
}

function indexProjection(projection: MonitorProjection): {
  roots: ProjectionNode[];
  byId: Map<string, ProjectionNode>;
} {
  if (projection.version !== 'monitor.v1') throw new Error('unsupported monitor projection');
  const byId = new Map<string, ProjectionNode>();
  for (const item of projection.components ?? []) {
    const component = item.component;
    const own = item.own_evaluation;
    if (!component?.id || byId.has(component.id)) throw new Error('invalid or duplicate component');
    if (component.archived) continue;
    if (!own || own.component_id !== component.id || !isStatus(own.status)) {
      throw new Error(`component ${component.id} is missing its own evaluation`);
    }
    byId.set(component.id, { component, own, children: [] });
  }
  const roots: ProjectionNode[] = [];
  for (const node of byId.values()) {
    const parentId = node.component.parent_id;
    if (!parentId) {
      roots.push(node);
      continue;
    }
    const parent = byId.get(parentId);
    if (!parent) throw new Error(`component ${node.component.id} has a missing parent`);
    parent.children.push(node);
  }
  const visiting = new Set<string>();
  const visited = new Set<string>();
  const visit = (node: ProjectionNode) => {
    const id = node.component.id;
    if (visiting.has(id)) throw new Error(`component cycle includes ${id}`);
    if (visited.has(id)) return;
    visiting.add(id);
    for (const child of node.children) visit(child);
    visiting.delete(id);
    visited.add(id);
  };
  for (const node of byId.values()) visit(node);
  if (visited.size !== byId.size) throw new Error('component graph is disconnected');
  const order = (a: ProjectionNode, b: ProjectionNode) =>
    (a.component.sort_order ?? 0) - (b.component.sort_order ?? 0) ||
    a.component.id.localeCompare(b.component.id);
  roots.sort(order);
  for (const node of byId.values()) node.children.sort(order);
  return { roots, byId };
}

function worst(a: MonitorStatus, b: MonitorStatus): MonitorStatus {
  return STATUS_RANK[a] >= STATUS_RANK[b] ? a : b;
}

function effective(node: ProjectionNode, memo = new Map<string, MonitorStatus>()): MonitorStatus {
  const cached = memo.get(node.component.id);
  if (cached) return cached;
  let criticalChildren: MonitorStatus = 'operational';
  let otherChildren: MonitorStatus = 'operational';
  for (const child of node.children) {
    const status = effective(child, memo);
    if (child.component.critical || child.component.kind === 'critical') {
      criticalChildren = worst(criticalChildren, status);
    } else {
      otherChildren = worst(otherChildren, status);
    }
  }
  if (otherChildren === 'outage') otherChildren = 'degraded';
  const status = worst(node.own.status, worst(criticalChildren, otherChildren));
  memo.set(node.component.id, status);
  return status;
}

function subtreeIDs(node: ProjectionNode): Set<string> {
  const ids = new Set<string>([node.component.id]);
  for (const child of node.children) for (const id of subtreeIDs(child)) ids.add(id);
  return ids;
}

function activeIncidents(projection: MonitorProjection) {
  return (projection.incidents ?? []).filter((incident) =>
    incident.status !== 'resolved' && !incident.resolved_at_unix,
  );
}

function assertReferences(projection: MonitorProjection, byId: Map<string, ProjectionNode>) {
  for (const incident of activeIncidents(projection)) {
    if (!incident.id || !incident.affects?.length || incident.affects.some((id) => !byId.has(id))) {
      throw new Error(`incident ${incident.id || '<missing>'} references an invalid component`);
    }
  }
  for (const maintenance of projection.maintenance ?? []) {
    if (!maintenance.id || !maintenance.affects?.length || maintenance.affects.some((id) => !byId.has(id))) {
      throw new Error(`maintenance ${maintenance.id || '<missing>'} references an invalid component`);
    }
  }
}

function rootFor(projection: MonitorProjection, scope: string | null) {
  const indexed = indexProjection(projection);
  assertReferences(projection, indexed.byId);
  const root = indexed.byId.get(rootComponentId(scope));
  if (!root) throw new Error('scope root is missing from owner projection');
  return { ...indexed, root };
}

function incidentRows(projection: MonitorProjection, node: ProjectionNode) {
  return activeIncidents(projection)
    .filter((incident) => incident.affects.includes(node.component.id))
    .sort((a, b) => b.started_at_unix - a.started_at_unix || a.id.localeCompare(b.id));
}

export function buildPulpSummaryTree(
  projection: MonitorProjection,
  scope: string | null,
): SummaryNode {
  const { root } = rootFor(projection, scope);
  const memo = new Map<string, MonitorStatus>();
  const allActive = activeIncidents(projection);
  const build = (node: ProjectionNode): SummaryNode => {
    const ids = subtreeIDs(node);
    return {
      id: node.component.id,
      name: node.component.name,
      kind: node.component.tag || node.component.kind,
      status: effective(node, memo) as ServiceStatus,
      issueCount: allActive.filter((incident) => incident.affects.some((id) => ids.has(id))).length,
      incidents: incidentRows(projection, node).map((incident) => ({
        id: incident.id,
        title: incident.title,
        severity: incident.severity,
        status: incident.status,
        auto: incident.auto !== false,
        started: iso(incident.started_at_unix),
      })),
      children: node.children.map(build),
    };
  };
  return build(root);
}

export function buildPulpStatusJSON(
  projection: MonitorProjection,
  scope: string | null,
  now = new Date(),
) {
  const { root, byId } = rootFor(projection, scope);
  const memo = new Map<string, MonitorStatus>();
  const ids = subtreeIDs(root);
  const productFor = (node: ProjectionNode) => {
    let current: ProjectionNode | undefined = node;
    let fallback = root.component.id;
    while (current) {
      if (current.component.kind === 'product') return current.component.id;
      current = current.component.parent_id ? byId.get(current.component.parent_id) : undefined;
    }
    return fallback;
  };
  const services = [...ids]
    .map((id) => byId.get(id)!)
    .filter((node) =>
      node.children.length === 0 &&
      (node.component.kind === 'service' || node.component.kind === 'host' || node.component.kind === 'critical'),
    )
    .map((node) => ({
      id: node.component.id,
      name: node.component.name,
      product: productFor(node),
      status: effective(node, memo) as ServiceStatus,
    }));
  const active = activeIncidents(projection)
    .filter((incident) => incident.affects.some((id) => ids.has(id)))
    .sort((a, b) => b.started_at_unix - a.started_at_unix || a.id.localeCompare(b.id))
    .map((incident) => ({
      id: incident.id,
      title: incident.title,
      severity: incident.severity,
      status: incident.status,
      started: iso(incident.started_at_unix),
    }));
  const nowUnix = Math.floor(now.getTime() / 1000);
  const maintenance = (projection.maintenance ?? [])
    .filter((item) =>
      !item.cancelled &&
      item.scheduled_end_unix >= nowUnix &&
      item.affects.some((id) => ids.has(id)),
    )
    .sort((a, b) => a.scheduled_start_unix - b.scheduled_start_unix || a.id.localeCompare(b.id))
    .map((item) => ({
      id: item.id,
      title: item.title,
      scheduledStart: iso(item.scheduled_start_unix),
      scheduledEnd: iso(item.scheduled_end_unix),
    }));
  const status = effective(root, memo) as ServiceStatus;
  return {
    status,
    state: statusToState(status),
    services,
    activeIncidents: active,
    scheduledMaintenance: maintenance,
  };
}

type OwnerIncident = MonitorProjection['incidents'][number];

const DAY_RANK: Record<string, number> = { ok: 0, maint: 1, deg: 2, out: 3 };
const RANK_DAY = ['ok', 'maint', 'deg', 'out'];
const STATUS_DAY: Record<MonitorStatus, string> = {
  operational: 'ok',
  degraded: 'deg',
  outage: 'out',
};
const SEVERITY_DAY: Record<string, string> = { major: 'out', moderate: 'deg' };
const INCIDENT_SEVERITY_RANK: Record<string, number> = { major: 3, moderate: 2, minor: 1 };

function assertAllReferences(projection: MonitorProjection, byId: Map<string, ProjectionNode>) {
  for (const incident of projection.incidents ?? []) {
    if (!incident.id ||
        !isIncidentStatus(incident.status) ||
        !isIncidentSeverity(incident.severity) ||
        (incident.status === 'resolved') !== Boolean(incident.resolved_at_unix) ||
        !incident.affects?.length ||
        incident.affects.some((id) => !byId.has(id))) {
      throw new Error(`incident ${incident.id || '<missing>'} references an invalid component`);
    }
  }
  for (const update of projection.incident_updates ?? []) {
    if (!update.id || !update.incident_id ||
        !(projection.incidents ?? []).some((incident) => incident.id === update.incident_id)) {
      throw new Error(`incident update ${update.id || '<missing>'} references an invalid incident`);
    }
  }
  for (const maintenance of projection.maintenance ?? []) {
    if (!maintenance.id || !maintenance.affects?.length ||
        maintenance.affects.some((id) => !byId.has(id))) {
      throw new Error(`maintenance ${maintenance.id || '<missing>'} references an invalid component`);
    }
  }
}

function publicProjection(projection: MonitorProjection) {
  const indexed = indexProjection(projection);
  assertAllReferences(projection, indexed.byId);
  return indexed;
}

function chainTo(byId: Map<string, ProjectionNode>, id: string): ProjectionNode[] {
  const result: ProjectionNode[] = [];
  const seen = new Set<string>();
  let current = byId.get(id);
  while (current && !seen.has(current.component.id)) {
    seen.add(current.component.id);
    result.unshift(current);
    current = current.component.parent_id
      ? byId.get(current.component.parent_id)
      : undefined;
  }
  return result;
}

function hrefFor(byId: Map<string, ProjectionNode>, id: string, rootId: string): string {
  const chain = chainTo(byId, id).map((node) => node.component.id);
  const rootIndex = chain.indexOf(rootId);
  const segments = rootIndex >= 0 ? chain.slice(rootIndex + 1) : chain;
  return segments.length ? `/${segments.join('/')}` : '/';
}

function resolvePath(root: ProjectionNode, segments: string[]): ProjectionNode | null {
  let current = root;
  for (const segment of segments) {
    const child = current.children.find((candidate) => candidate.component.id === segment);
    if (!child) return null;
    current = child;
  }
  return current;
}

function productFor(byId: Map<string, ProjectionNode>, componentId: string): string {
  let current = byId.get(componentId);
  let organization = rootComponentId(null);
  while (current) {
    if (current.component.kind === 'product') return current.component.id;
    if (current.component.kind === 'organization') organization = current.component.id;
    current = current.component.parent_id
      ? byId.get(current.component.parent_id)
      : undefined;
  }
  return organization;
}

function ownerTimeline(projection: MonitorProjection, incidentId: string): TimelineEntry[] {
  return (projection.incident_updates ?? [])
    .filter((update) => update.incident_id === incidentId)
    .sort((left, right) => right.at_unix - left.at_unix || left.id.localeCompare(right.id))
    .map((update) => ({
      status: update.label.toLowerCase() as IncidentStatus,
      body: update.body,
      timestamp: iso(update.at_unix),
    }));
}

function ownerIncident(
  projection: MonitorProjection,
  byId: Map<string, ProjectionNode>,
  incident: OwnerIncident,
  includeTimeline = true,
): Incident {
  const firstAffected = incident.affects[0];
  return {
    id: incident.id,
    title: incident.title,
    severity: incident.severity as IncidentSeverity,
    status: incident.status as IncidentStatus,
    product: firstAffected ? productFor(byId, firstAffected) : rootComponentId(null),
    affects: [...incident.affects],
    auto: incident.auto !== false,
    started: iso(incident.started_at_unix),
    resolved: incident.resolved_at_unix ? iso(incident.resolved_at_unix) : undefined,
    timeline: includeTimeline ? ownerTimeline(projection, incident.id) : [],
  };
}

function scopedIncidentRows(
  projection: MonitorProjection,
  scope: string | null,
  includeTimeline = true,
): Incident[] {
  const { byId } = publicProjection(projection);
  const root = byId.get(rootComponentId(scope));
  if (!root) throw new Error('scope root is missing from owner projection');
  const ids = subtreeIDs(root);
  return (projection.incidents ?? [])
    .filter((incident) => incident.affects.some((id) => ids.has(id)))
    .sort((left, right) => right.started_at_unix - left.started_at_unix || left.id.localeCompare(right.id))
    .map((incident) => ownerIncident(projection, byId, incident, includeTimeline));
}

export function buildPulpIncidentHistory(
  projection: MonitorProjection,
  scope: string | null,
  limit = 50,
  offset = 0,
): { incidents: Incident[]; total: number } {
  const incidents = scopedIncidentRows(projection, scope, true);
  return {
    total: incidents.length,
    incidents: incidents.slice(offset, offset + limit),
  };
}

export function buildPulpIncident(
  projection: MonitorProjection,
  incidentId: string,
): Incident | undefined {
  const { byId } = publicProjection(projection);
  const incident = (projection.incidents ?? []).find((candidate) => candidate.id === incidentId);
  return incident ? ownerIncident(projection, byId, incident) : undefined;
}

export function buildPulpComponentCrumbs(
  projection: MonitorProjection,
  scope: string | null,
  componentId: string,
): { crumbs: CrumbItem[]; affectedPath: string[] } {
  const { byId } = publicProjection(projection);
  const rootId = rootComponentId(scope);
  if (!byId.has(componentId) || !byId.has(rootId)) return { crumbs: [], affectedPath: [] };
  const chain = chainTo(byId, componentId);
  const rootIndex = chain.findIndex((node) => node.component.id === rootId);
  const visible = rootIndex >= 0 ? chain.slice(rootIndex) : chain;
  return {
    crumbs: visible.map((node) => ({
      label: node.component.name,
      href: hrefFor(byId, node.component.id, rootId),
    })),
    affectedPath: visible.map((node) => node.component.name),
  };
}

function windowDates(now: Date): string[] {
  const today = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()));
  const result: string[] = [];
  for (let daysAgo = 89; daysAgo >= 0; daysAgo--) {
    const date = new Date(today);
    date.setUTCDate(today.getUTCDate() - daysAgo);
    result.push(date.toISOString().slice(0, 10));
  }
  return result;
}

function windowedDays(stored: unknown, dates: string[]): string[] {
  const byDate = new Map<string, string>();
  if (Array.isArray(stored)) {
    for (const entry of stored) {
      if (entry && typeof entry === 'object' && 'date' in entry && 'status' in entry) {
        byDate.set(String(entry.date), String(entry.status));
      }
    }
  }
  return dates.map((date) => byDate.get(date) ?? 'future');
}

function derivedUptime(
  node: ProjectionNode,
  dates: string[],
  memo: Map<string, string[]>,
): string[] {
  const cached = memo.get(node.component.id);
  if (cached) return cached;
  const own = windowedDays(node.component.uptime_90d, dates);
  let result: string[];
  if (node.children.length === 0) {
    result = own;
  } else {
    const children = node.children.map((child) => ({
      critical: child.component.critical || child.component.kind === 'critical',
      days: derivedUptime(child, dates, memo),
    }));
    result = dates.map((_, index) => {
      const ownRank = DAY_RANK[own[index]] ?? -1;
      let criticalRank = -1;
      let otherRank = -1;
      let hasData = ownRank >= 0;
      for (const child of children) {
        const rank = DAY_RANK[child.days[index]] ?? -1;
        if (rank < 0) continue;
        hasData = true;
        if (child.critical) criticalRank = Math.max(criticalRank, rank);
        else otherRank = Math.max(otherRank, rank);
      }
      if (!hasData) return 'future';
      const cappedOther = otherRank === DAY_RANK.out ? DAY_RANK.deg : otherRank;
      const rank = Math.max(ownRank, criticalRank, cappedOther);
      return rank < 0 ? 'ok' : RANK_DAY[rank];
    });
  }
  memo.set(node.component.id, result);
  return result;
}

function overlayIncidentDays(
  days: string[],
  subtree: Set<string>,
  incidents: Incident[],
  dates: string[],
): string[] {
  const result = [...days];
  const today = dates[dates.length - 1];
  for (const incident of incidents) {
    const paint = SEVERITY_DAY[incident.severity];
    if (!paint || !(incident.affects ?? []).some((id) => subtree.has(id))) continue;
    const start = incident.started.slice(0, 10);
    const end = incident.resolved?.slice(0, 10) ?? today;
    for (let index = 0; index < dates.length; index++) {
      if (dates[index] >= start && dates[index] <= end &&
          (DAY_RANK[paint] ?? 0) > (DAY_RANK[result[index]] ?? -1)) {
        result[index] = paint;
      }
    }
  }
  return result;
}

function withLiveToday(days: string[], status: MonitorStatus): string[] {
  const result = [...days];
  if (result.length) result[result.length - 1] = STATUS_DAY[status];
  return result;
}

function uptimePercent(days: string[]): number {
  const withData = days.filter((day) => day && day !== 'future');
  if (!withData.length) return 100;
  return Math.round((withData.filter((day) => day === 'ok').length / withData.length) * 1000) / 10;
}

function recurringMaintenance(now: Date): MaintWindow[] {
  const day = now.getUTCDay();
  if (day !== 1 && day !== 2) return [];
  const start = new Date(now);
  start.setUTCDate(now.getUTCDate() + (2 - day));
  start.setUTCHours(20, 0, 0, 0);
  const end = new Date(start);
  end.setUTCHours(23, 59, 0, 0);
  return [{
    id: 'recurring-tuesday',
    title: 'Weekly maintenance',
    summary: 'Routine maintenance may occur Tuesday evening — brief disruptions possible.',
    start: start.toISOString(),
    end: end.toISOString(),
    kind: 'scheduled',
    active: false,
  }];
}

function subtreeMaintenance(
  projection: MonitorProjection,
  node: ProjectionNode,
  now: Date,
): MaintWindow[] {
  const ids = subtreeIDs(node);
  const nowUnix = Math.floor(now.getTime() / 1000);
  return (projection.maintenance ?? [])
    .filter((maintenance) =>
      !maintenance.cancelled &&
      maintenance.scheduled_end_unix >= nowUnix &&
      maintenance.affects.some((id) => ids.has(id)),
    )
    .sort((left, right) =>
      left.scheduled_start_unix - right.scheduled_start_unix || left.id.localeCompare(right.id),
    )
    .map((maintenance) => ({
      id: maintenance.id,
      title: maintenance.title,
      summary: maintenance.summary,
      start: iso(maintenance.scheduled_start_unix),
      end: iso(maintenance.scheduled_end_unix),
      kind: maintenance.kind || 'scheduled',
      active: maintenance.scheduled_start_unix <= nowUnix &&
        maintenance.scheduled_end_unix >= nowUnix,
    }));
}

function dayIncidents(
  incidents: Incident[],
  node: ProjectionNode,
  byId: Map<string, ProjectionNode>,
  now: Date,
): DayIncident[] {
  const ids = subtreeIDs(node);
  const today = now.toISOString().slice(0, 10);
  return incidents
    .filter((incident) => (incident.affects ?? []).some((id) => ids.has(id)))
    .map((incident) => {
      const affected = (incident.affects ?? []).find((id) => ids.has(id));
      return {
        id: incident.id,
        title: incident.title,
        severity: incident.severity,
        affectedName: affected ? byId.get(affected)?.component.name ?? affected : undefined,
        affects: (incident.affects ?? []).filter((id) => ids.has(id)),
        start: incident.started.slice(0, 10),
        end: incident.resolved?.slice(0, 10) ?? today,
      };
    });
}

function componentPageIncidents(
  projection: MonitorProjection,
  byId: Map<string, ProjectionNode>,
): Incident[] {
  const ordered = (rows: OwnerIncident[]) => [...rows]
    .sort((left, right) =>
      right.started_at_unix - left.started_at_unix || left.id.localeCompare(right.id),
    )
    .map((incident) => ownerIncident(projection, byId, incident));
  const active = ordered((projection.incidents ?? []).filter((incident) =>
    incident.status !== 'resolved' && !incident.resolved_at_unix,
  ));
  const resolved = ordered((projection.incidents ?? []).filter((incident) =>
    incident.status === 'resolved' || Boolean(incident.resolved_at_unix),
  )).slice(0, 300);
  return [...active, ...resolved];
}

export function buildPulpComponentView(
  projection: MonitorProjection,
  scope: string | null,
  segments: string[],
  now = new Date(),
): ScopeView | null {
  const { byId } = publicProjection(projection);
  const rootId = rootComponentId(scope);
  const root = byId.get(rootId);
  if (!root) throw new Error('scope root is missing from owner projection');
  const node = resolvePath(root, segments);
  if (!node) return null;

  const nodeID = node.component.id;
  const statusMemo = new Map<string, MonitorStatus>();
  const status = effective(node, statusMemo);
  const dates = windowDates(now);
  const uptimeMemo = new Map<string, string[]>();
  const allIncidents = componentPageIncidents(projection, byId);
  const active = allIncidents.filter((incident) => incident.status !== 'resolved' && !incident.resolved);
  const children: ViewChild[] = node.children.map((child) => {
    const childStatus = effective(child, statusMemo);
    const subtree = [...subtreeIDs(child)];
    const uptime = withLiveToday(
      overlayIncidentDays(
        derivedUptime(child, dates, uptimeMemo),
        new Set(subtree),
        allIncidents,
        dates,
      ),
      childStatus,
    );
    return {
      id: child.component.id,
      name: child.component.name,
      kind: (child.component.tag || child.component.kind) as ViewChild['kind'],
      status: childStatus as ServiceStatus,
      issueCount: active.filter((incident) =>
        (incident.affects ?? []).some((id) => subtree.includes(id)),
      ).length,
      maintCount: subtreeMaintenance(projection, child, now).length,
      href: hrefFor(byId, child.component.id, rootId),
      uptime,
      uptimePct: uptimePercent(uptime),
      subtree,
    };
  });
  const chain = chainTo(byId, nodeID);
  const rootIndex = chain.findIndex((candidate) => candidate.component.id === rootId);
  const visible = rootIndex >= 0 ? chain.slice(rootIndex) : chain;
  const crumbs: CrumbItem[] = visible.map((candidate, index) => ({
    label: candidate.component.name,
    href: index < visible.length - 1
      ? hrefFor(byId, candidate.component.id, rootId)
      : undefined,
  }));
  const subtree = [...subtreeIDs(node)];
  const subtreeSet = new Set(subtree);
  const attachedIncidents = active.filter((incident) => incident.affects?.includes(nodeID));
  const subtreeIncidents = active
    .filter((incident) => (incident.affects ?? []).some((id) => subtreeSet.has(id)))
    .map((incident) => {
      const affected = (incident.affects ?? []).find((id) => subtreeSet.has(id));
      return {
        ...incident,
        affectedName: affected ? byId.get(affected)?.component.name ?? affected : undefined,
      };
    })
    .sort((left, right) =>
      (INCIDENT_SEVERITY_RANK[right.severity] ?? 0) -
      (INCIDENT_SEVERITY_RANK[left.severity] ?? 0),
    );
  const uptime = withLiveToday(
    overlayIncidentDays(
      derivedUptime(node, dates, uptimeMemo),
      subtreeSet,
      allIncidents,
      dates,
    ),
    status,
  );
  const maintenance = [
    ...recurringMaintenance(now),
    ...subtreeMaintenance(projection, node, now),
  ];
  return {
    status: status as ServiceStatus,
    state: statusToState(status as ServiceStatus),
    isRoot: nodeID === rootId,
    nodeName: node.component.name,
    nodeTag: node.component.tag || undefined,
    level: node.component.kind === 'organization'
      ? 'umbrella'
      : node.component.kind === 'product'
        ? 'product'
        : 'service',
    crumbs,
    children,
    attachedIncidents,
    subtreeIncidents,
    dayIncidents: dayIncidents(allIncidents, node, byId, now),
    subtreeIds: subtree,
    issueCount: subtreeIncidents.length,
    maintCount: maintenance.length,
    affectedChildNames: children
      .filter((child) => child.status !== 'operational')
      .map((child) => child.name),
    maintenance,
    uptime,
    uptimePct: uptimePercent(uptime),
  };
}
