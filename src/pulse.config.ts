/**
 * ── THE INSTANCE SEAM (Banana Pulse) ───────────────────────────────────────
 *
 * Everything company-specific lives HERE, plus two siblings:
 *   - /public/brand/*        (the logos)
 *   - src/styles/brand.css   (the per-scope accent palette)
 *
 * The rest of the codebase is brandless **Pulse** — the quorum engine, the
 * ingest contract, the one-design skin, the resource-driven admin. It knows
 * nothing about any brand; it reads this config.
 *
 * To run your company's status page: edit this file + swap brand.css + the
 * /public/brand assets (and keep this repo as your git upstream so engine
 * updates merge down cleanly — your changes should only ever touch the seam).
 */

export interface ScopeConfig {
  /** Scope id — also the id of this scope's landing-root component. */
  id: string;
  /** Host header that lands on this scope. */
  host: string;
  /** The umbrella/root scope (resolveScope returns null for it publicly). */
  umbrella?: boolean;
  wordmark: string;
  /** Logo served from /public/brand. */
  logo: string;
}

export const COMPANY = 'Example Co';
export const COMPANY_LEGAL = '© 2026 Example Co';
export const FOOTER_DOMAINS = ['example.com'];

/** Feed/page metadata, derived from the spaced company name (NOT a second source). */
export const SITE_TITLE = `${COMPANY} Status`;
export const SITE_DESCRIPTION = `Real-time status and incident history for ${COMPANY} services.`;
export const SUPPORT_EMAIL = 'status@example.com';
/** localStorage key for the public page's light/dark preference. */
export const THEME_STORAGE_KEY = 'pulse-status-theme';

export const SCOPES: ScopeConfig[] = [
  { id: 'example', host: 'status.example.com', umbrella: true, wordmark: 'Example Co', logo: '/brand/pulse.svg' },
];

export const UMBRELLA_ID = SCOPES.find((s) => s.umbrella)!.id;
/** The umbrella status host (for absolute URLs in feeds / permalinks). */
export const STATUS_DOMAIN = SCOPES.find((s) => s.umbrella)!.host;

const byHost = new Map(SCOPES.map((s) => [s.host, s]));
const byId = new Map(SCOPES.map((s) => [s.id, s]));

/** Host → public scope id; null = umbrella. */
export function scopeForHost(host: string): string | null {
  const hostname = host.split(':')[0];
  const s = byHost.get(hostname);
  return !s || s.umbrella ? null : s.id;
}

/** Public scope (null = umbrella) → the landing-root component id. */
export function rootComponentId(scope: string | null): string {
  return scope && byId.has(scope) ? scope : UMBRELLA_ID;
}

/** Public scope (null = umbrella) → brand identity (wordmark + logo). */
export function scopeBrand(scope: string | null): ScopeConfig {
  return byId.get(scope ?? UMBRELLA_ID) ?? SCOPES[0];
}
