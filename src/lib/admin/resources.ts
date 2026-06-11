/**
 * Resource declarations — the generic CMS spine.
 *
 * Every manageable thing in the admin is described HERE as a declaration
 * (label, fields with types, allowed actions), not as a bespoke screen. The
 * generic list → detail → form views read these declarations and render
 * themselves; adding a new manageable resource later is a new declaration plus
 * a data adapter, never a new screen. This is the reusable CMS seed for other
 * properties (Evolution next) — nothing here is status-specific beyond the
 * declarations themselves.
 *
 * Discipline note (status §5): the admin is narration + scheduling +
 * configuration + override — never manual status-flipping. So no resource here
 * exposes a "set live status" field; incident state moves flow through the
 * engine via recordManualOverride. Components are config (the tree), not a
 * place to hand-set whether something is up.
 */

export type FieldType =
  | 'text'
  | 'textarea'
  | 'select'
  | 'number'
  | 'boolean'
  | 'datetime'
  | 'color'
  | 'tags'        // free-list of strings (e.g. affected component ids)
  | 'multiselect' // checkbox set against options
  | 'readonly';   // shown, never edited

export interface FieldOption {
  value: string;
  label: string;
}

export interface FieldDecl {
  name: string;
  label: string;
  type: FieldType;
  hint?: string;
  required?: boolean;
  /** Static options; dynamic option sets are injected at render time by key. */
  options?: FieldOption[];
  /** Name of a dynamic option set resolved by the page (e.g. 'products'). */
  optionsKey?: string;
  placeholder?: string;
  /** Hide from the create form (e.g. system/derived fields). */
  createOnly?: boolean;
  /** Hide from the list table (show only in detail/form). */
  detailOnly?: boolean;
}

/** Actions a resource supports beyond plain list/detail. */
export interface ResourceActions {
  create?: boolean;
  edit?: boolean;
  archive?: boolean; // soft-delete (archive, don't delete) where supported
  delete?: boolean;
}

export interface ResourceDecl {
  /** URL key under /admin and /api/v1/admin. */
  key: string;
  /** Singular + plural labels for chrome. */
  label: string;
  labelPlural: string;
  /** Sidebar icon glyph (matches the mock's small square markers). */
  icon: string;
  /** One-line description shown atop the resource. */
  blurb: string;
  /** Read-only resources render no create/edit form. */
  readOnly?: boolean;
  /** Column field names for the list table (subset of fields). */
  listColumns: string[];
  fields: FieldDecl[];
  actions: ResourceActions;
  /** Filter tabs shown above the list, if any (value → label). */
  tabs?: { value: string; label: string }[];
}

/* ── status vocabulary shared by declarations ──────────────────── */

export const SEVERITY_OPTIONS: FieldOption[] = [
  { value: 'minor', label: 'Minor — cosmetic / low impact' },
  { value: 'moderate', label: 'Moderate — some features affected' },
  { value: 'major', label: 'Major — core functionality down' },
];

export const INCIDENT_STATUS_OPTIONS: FieldOption[] = [
  { value: 'investigating', label: 'Investigating' },
  { value: 'identified', label: 'Identified' },
  { value: 'monitoring', label: 'Monitoring' },
  { value: 'resolved', label: 'Resolved' },
];

export const COMPONENT_KIND_OPTIONS: FieldOption[] = [
  { value: 'organization', label: 'Organization (root)' },
  { value: 'product', label: 'Product / group' },
  { value: 'service', label: 'Service' },
  { value: 'host', label: 'Host' },
];

export const SOURCE_KIND_OPTIONS: FieldOption[] = [
  { value: 'push', label: 'Push — POSTs observations' },
  { value: 'probe', label: 'Probe — external check' },
  { value: 'heartbeat', label: 'Heartbeat — must report or go stale' },
];

/* ── declarations ──────────────────────────────────────────────── */

export const RESOURCES: ResourceDecl[] = [
  {
    key: 'dashboard',
    label: 'Dashboard',
    labelPlural: 'Dashboard',
    icon: '◆',
    blurb: 'Situational view — what the engine sees right now.',
    readOnly: true,
    listColumns: [],
    fields: [],
    actions: {},
  },
  {
    key: 'incidents',
    label: 'Incident',
    labelPlural: 'Incidents',
    icon: '▲',
    blurb:
      'The engine opens and closes incidents; here you narrate, move status, override level, or declare one the monitors missed.',
    listColumns: ['title', 'severity', 'status', 'startedAt'],
    tabs: [
      { value: 'active', label: 'Active' },
      { value: 'scheduled', label: 'Scheduled' },
      { value: 'resolved', label: 'Resolved' },
    ],
    fields: [
      { name: 'title', label: 'Title', type: 'text', required: true, placeholder: 'Game Server 3 unreachable', hint: 'Write for customers. Plain language, no jargon.' },
      { name: 'summary', label: 'Customer message', type: 'textarea', required: true, placeholder: "What customers are experiencing, what you're doing about it…" },
      { name: 'severity', label: 'Level', type: 'select', required: true, options: SEVERITY_OPTIONS, hint: 'Override the level the engine would infer.' },
      { name: 'affects', label: 'Affected component', type: 'multiselect', required: true, optionsKey: 'components', hint: 'A manual declare is a high-weight manual-source observation — it flows through the same engine.' },
      { name: 'status', label: 'Status', type: 'readonly', detailOnly: true },
    ],
    actions: { create: true, edit: true },
  },
  {
    key: 'maintenance',
    label: 'Maintenance window',
    labelPlural: 'Maintenance',
    icon: '◇',
    blurb: 'Schedule planned windows. Inherently manual — no monitor predicts maintenance.',
    listColumns: ['title', 'scheduledStart', 'scheduledEnd'],
    fields: [
      { name: 'title', label: 'Title', type: 'text', required: true, placeholder: 'Postmark DNS migration' },
      { name: 'summary', label: 'Customer message', type: 'textarea', required: true, placeholder: 'Email may be delayed up to 15 min during the window.' },
      { name: 'scheduledStart', label: 'Starts', type: 'datetime', required: true },
      { name: 'scheduledEnd', label: 'Ends', type: 'datetime', required: true },
      { name: 'affects', label: 'Affected component', type: 'multiselect', required: true, optionsKey: 'components' },
    ],
    actions: { create: true, edit: true, delete: true },
  },
  {
    key: 'sources',
    label: 'Source',
    labelPlural: 'Sources & Tokens',
    icon: '◈',
    blurb:
      'Register a source, issue/rotate its bearer token (shown once), set weight + default TTL, and map raw labels to components.',
    listColumns: ['name', 'kind', 'trusted', 'weight', 'defaultTtl'],
    fields: [
      { name: 'name', label: 'Name', type: 'text', required: true, placeholder: 'evolution-self-report' },
      { name: 'kind', label: 'Kind', type: 'select', required: true, options: SOURCE_KIND_OPTIONS },
      { name: 'trusted', label: 'Trusted (first-party)', type: 'boolean', placeholder: 'May declare on its own', hint: 'A trusted first-party vantage declares an incident alone (capped to degraded); a second source escalates it. Leave off for external validators, which only corroborate (alone they just WATCH).' },
      { name: 'weight', label: 'Trust weight', type: 'number', hint: 'Higher weight counts for more in quorum.', placeholder: '1' },
      { name: 'defaultTtl', label: 'Default TTL (seconds)', type: 'number', hint: 'How long an observation stays valid if it sets no explicit expiry. Blank = never expires (no dead-man).', placeholder: '300' },
    ],
    actions: { create: true, archive: true },
  },
  {
    key: 'components',
    label: 'Component',
    labelPlural: 'Components',
    icon: '●',
    blurb: 'Define the tree: organization / product / service / host, its parent, sort, and (for products) brand + domain. Setup config — archive, never delete. Status is derived by the engine, never set here.',
    listColumns: ['name', 'kind', 'tag'],
    tabs: [
      { value: 'active', label: 'Active' },
      { value: 'archived', label: 'Archived' },
    ],
    fields: [
      { name: 'id', label: 'ID', type: 'text', required: true, placeholder: 'provisioner', hint: 'Stable url-safe key — used in the public path, observations, and incident affects. Cannot change later.', createOnly: true },
      { name: 'name', label: 'Name', type: 'text', required: true, placeholder: 'Provisioner · Bananagine' },
      { name: 'kind', label: 'Kind', type: 'select', required: true, options: COMPONENT_KIND_OPTIONS, hint: 'Structural role in the tree.' },
      { name: 'parentId', label: 'Parent', type: 'select', optionsKey: 'componentParents', hint: 'Which node this rolls up into. Blank = top level (organization root).' },
      { name: 'tag', label: 'Tag', type: 'text', placeholder: 'repo · runtime', hint: 'Display label shown beside the name.' },
      { name: 'brand', label: 'Brand scope', type: 'text', placeholder: 'sessions', hint: 'Product nodes only — scope id that selects the brand (accent + wordmark + logo).' },
      { name: 'domain', label: 'Domain', type: 'text', placeholder: 'status.sessions.gg', hint: 'Product nodes only — Host header that lands on this scope.' },
      { name: 'sortOrder', label: 'Sort', type: 'number', hint: 'Lower numbers appear first.', placeholder: '0' },
    ],
    actions: { create: true, edit: true, archive: true },
  },
  {
    key: 'subscribers',
    label: 'Subscriber',
    labelPlural: 'Subscribers',
    icon: '◎',
    blurb: 'Endpoints notified when an incident opens or updates in their scope. Mostly read.',
    readOnly: true,
    listColumns: ['email', 'confirmedAt', 'createdAt'],
    fields: [
      { name: 'email', label: 'Endpoint', type: 'readonly' },
      { name: 'confirmedAt', label: 'Confirmed', type: 'readonly' },
      { name: 'createdAt', label: 'Subscribed', type: 'readonly' },
    ],
    actions: { delete: true },
  },
  {
    key: 'settings',
    label: 'Settings',
    labelPlural: 'Settings',
    icon: '⚙',
    blurb: 'Domains served, default landing scope per domain, and page titles.',
    listColumns: [],
    fields: [
      { name: 'pageTitle', label: 'Page title', type: 'text' },
      { name: 'domains', label: 'Domains served (Host-header scoping)', type: 'textarea', hint: 'One per line.' },
      { name: 'landingScopes', label: 'Default landing scope per domain', type: 'textarea', hint: 'domain → scope, one per line.' },
    ],
    actions: { edit: true },
  },
];

export function getResource(key: string): ResourceDecl | undefined {
  return RESOURCES.find((r) => r.key === key);
}

/** Resources that appear in the sidebar nav (everything, in declared order). */
export const NAV_RESOURCES = RESOURCES;
