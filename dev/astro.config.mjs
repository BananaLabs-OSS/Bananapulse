// dev/astro.config.mjs — standalone preview for working on Bananapulse
// itself. Runs as its own Astro site, installs the integration via
// `bananapulse: 'file:..'` (see dev/package.json), so this dev site is
// indistinguishable from a real consumer's setup.
//
// First-time setup: from this dir, `npm install` symlinks the workspace
// root as ./node_modules/bananapulse. Then `npm run dev` (or `astro dev`
// here) starts the preview at :4322.

import { defineConfig } from 'astro/config';
import bananapulse from 'bananapulse';

export default defineConfig({
  output: 'static',
  server: { port: 4322, host: '0.0.0.0' },
  integrations: [
    bananapulse({
      mountPath: '/status',
      name: 'Bananapulse Dev',
      domain: 'localhost:4322',
      sources: [
        // Stub upstream — change to a real endpoint to see real data.
        // Without a reachable URL the page renders an "unreachable" tile
        // (which is itself a useful UI state to preview).
        {
          url: 'http://localhost:4322/stub-upstream.json',
          type: 'queue-status',
        },
      ],
      incidentsPath: './data/incidents.json',
    }),
  ],
});
