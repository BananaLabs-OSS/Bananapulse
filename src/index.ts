// bananapulse — Astro integration entry.
//
// Consumers do:
//
//   import bananapulse from 'bananapulse';
//   export default defineConfig({
//     integrations: [bananapulse({ mountPath: '/status', sources: [...] })],
//   });
//
// This file is the package's main export. It re-exports the integration
// factory + public types so consumers never need to reach into deep
// subpaths.

export { default } from './integration.js';
export { default as bananapulse } from './integration.js';
export type {
  BananapulseConfig,
  BananapulseSource,
  BananapulseSourceType,
  BananapulseThemeMap,
} from './integration.js';
export type {
  Health,
  Subsystem,
  CanonicalStatus,
  Incident,
} from './lib/canonical.js';
