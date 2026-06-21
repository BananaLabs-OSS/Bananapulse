/**
 * View model types for the public status front-end.
 *
 * The single live builder is `buildComponentView` in components.ts, which
 * reads the `components` adjacency tree and produces the `ScopeView` shape
 * declared here. This file now owns only the shared view *types* (`ScopeView`,
 * `ViewChild`, `CrumbItem`). The `statusToState` helper is the canonical one in
 * types.ts — this file no longer carries a duplicate.
 */

import type {
  Incident,
  ServiceStatus,
} from './types';

/** A node in the rolled-up status tree (product or service). */
export interface ViewChild {
  id: string;
  name: string;
  /** 'product' | 'service' — for the skin to label the kind. */
  kind: 'product' | 'service';
  /** Effective (rolled-up) status of this child. */
  status: ServiceStatus;
  /** Count of active (outage|degraded) incidents in this child's subtree. */
  issueCount: number;
  /** Count of scheduled maintenance windows in this child's subtree. */
  maintCount: number;
  /** Link to drill into this child (skin-agnostic; relative to the scope). */
  href: string;
  /** 90-day day-status history (DayStatus[]) for the uptime bar strip. */
  uptime: string[];
  /** Reliability % over the days that have data (no-data days excluded). */
  uptimePct: number;
}

export interface CrumbItem {
  label: string;
  href?: string;
}

/** A maintenance window relevant to the current scope (for the banner). */
export interface MaintWindow {
  id: string;
  title: string;
  summary: string;
  start: string; // ISO 8601
  end: string;   // ISO 8601
  kind: string;  // 'scheduled' | 'emergency'
  active: boolean; // now within [start, end]
}

export interface ScopeView {
  /** Effective rolled-up status for the whole scope. */
  status: ServiceStatus;
  /** Body-state class (working|degraded|outage|queued). */
  state: string;
  /** True for the landing root (umbrella or a product-scoped domain root). */
  isRoot: boolean;
  /** Display name of the current node (product or service name; '' at umbrella root). */
  nodeName: string;
  /** The node's own descriptive tag (e.g. 'web', 'api.example.com · backend')
   *  — shown in the banner instead of a (duplicate) breadcrumb. */
  nodeTag?: string;
  /** 'umbrella' | 'product' | 'service' — what level we're at. */
  level: 'umbrella' | 'product' | 'service';
  /** Tappable breadcrumb from landing root to here. */
  crumbs: CrumbItem[];
  /** Direct children with rolled-up status (empty for a leaf service). */
  children: ViewChild[];
  /** Active incidents attached AT this node (its own, not descendants'). */
  attachedIncidents: Incident[];
  /** ALL active incidents anywhere in this node's subtree, worst-first, each tagged
   *  with the affected component name — the "what's wrong below me" quick list. */
  subtreeIncidents: Incident[];
  /** Total active issue count across this scope's subtree. */
  issueCount: number;
  /** Total scheduled-maintenance count across this scope's subtree. */
  maintCount: number;
  /** Names of children that are not operational (for headline copy). */
  affectedChildNames: string[];
  /** Active + upcoming maintenance windows touching this scope (for the banner). */
  maintenance: MaintWindow[];
  /** This node's own 90-day day-status history (DayStatus[]) for its bar strip. */
  uptime: string[];
  /** This node's reliability % over the days that have data. */
  uptimePct: number;
}