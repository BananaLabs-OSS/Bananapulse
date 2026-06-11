/**
 * Resolves a hostname to a public scope (null = umbrella). Thin wrapper over the
 * instance seam — the host→scope map lives in pulse.config.
 */
import { scopeForHost } from '@/pulse.config';

export function resolveScope(host: string): string | null {
  return scopeForHost(host);
}
