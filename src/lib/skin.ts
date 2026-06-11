/**
 * Public-skin brand helpers. Thin wrappers over the instance seam
 * (pulse.config) — the wordmarks/logos/accents are config + brand.css, never
 * hard-coded in the skin.
 */
import { rootComponentId, scopeBrand, type ScopeConfig } from '@/pulse.config';

/** `data-scope` attribute value for a public scope (umbrella id when null). */
export function scopeId(scope: string | null): string {
  return rootComponentId(scope);
}

/** Brand identity (wordmark + logo) for a public scope. */
export function brandForScope(scope: string | null): ScopeConfig {
  return scopeBrand(scope);
}
