// incidents.ts — build-time incidents loader.
//
// In integration mode, the consumer points `incidentsPath` at a JSON
// file in their own project (default: ./src/data/incidents.json). The
// integration resolves that to an absolute path at config:setup time
// and ships it via the virtual:bananapulse/config module, so the
// injected routes do not depend on process.cwd().
//
// This module is imported by routes/index.astro and routes/incidents.astro
// at build time (Astro SSG runs it in Node), so it's a plain fs read.
// The routes are statically rendered — no runtime server is involved.

import { readFileSync, existsSync } from 'node:fs';
import { assertIncident, type Incident } from './canonical.js';

/**
 * Read and sort incidents from an absolute file path. Returns [] if
 * the file is missing, unparseable, or wrong-typed. Malformed entries
 * are skipped silently — the operator sees the gap on the rendered
 * page rather than a build failure.
 */
export function loadIncidents(absPath: string): Incident[] {
  if (!existsSync(absPath)) return [];
  let parsed: unknown;
  try {
    parsed = JSON.parse(readFileSync(absPath, 'utf8'));
  } catch {
    return [];
  }
  if (!Array.isArray(parsed)) return [];
  const out: Incident[] = [];
  for (const i of parsed) {
    try {
      assertIncident(i);
      out.push(i);
    } catch {
      // Skip malformed entries silently.
    }
  }
  return out.sort((a, b) => b.startedAt.localeCompare(a.startedAt));
}

export function listIncidents(absPath: string): Incident[] {
  return loadIncidents(absPath);
}

export function activeIncidents(absPath: string): Incident[] {
  return loadIncidents(absPath).filter((i) => !i.resolvedAt);
}
