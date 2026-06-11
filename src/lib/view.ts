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
}

export interface CrumbItem {
  label: string;
  href?: string;
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
  /** 'umbrella' | 'product' | 'service' — what level we're at. */
  level: 'umbrella' | 'product' | 'service';
  /** Tappable breadcrumb from landing root to here. */
  crumbs: CrumbItem[];
  /** Direct children with rolled-up status (empty for a leaf service). */
  children: ViewChild[];
  /** Active incidents attached AT this node (its own, not descendants'). */
  attachedIncidents: Incident[];
  /** Total active issue count across this scope's subtree. */
  issueCount: number;
  /** Total scheduled-maintenance count across this scope's subtree. */
  maintCount: number;
  /** Names of children that are not operational (for headline copy). */
  affectedChildNames: string[];
}