# Bananapulse

Self-deploy status page as an **Astro integration**. Install the
package, add one line to your `astro.config.mjs`, get a status page +
incident history + Atom feed at the mount path you choose.

Pure static. No SSR, no edge functions, no server runtime. Builds to
plain HTML/CSS/JS. Deploys on Cloudflare Pages, GitHub Pages, Netlify,
Vercel, S3+CloudFront, nginx, Caddy — anywhere that can serve static
files.

## What you get

- A live status overview at `${mountPath}` (default `/status`). The
  browser polls a configured upstream JSON endpoint every 30s and
  patches the DOM.
- An incident history page at `${mountPath}/incidents`, rendered from a
  committed JSON file at build time.
- An Atom 1.0 feed at `${mountPath}/incidents.xml`, materialised at
  build time so feed readers can subscribe.
- Zero server-side code on the deploy target. The "engine" is the
  browser running the page.

## Install

```bash
npm install bananapulse
```

## Use

```js
// astro.config.mjs
import { defineConfig } from 'astro/config';
import bananapulse from 'bananapulse';

export default defineConfig({
  integrations: [
    bananapulse({
      mountPath: '/status',
      name: 'My Service',
      domain: 'example.com',
      sources: [
        { url: 'https://api.example.com/status', type: 'queue-status' },
      ],
      // optional: incidentsPath, themeCssVarMap, themeCssPath
    }),
  ],
});
```

See `examples/minimal-config.md` and `examples/sessions-style-config.md`
for copy-paste-ready setups.

## Config shape

| Field            | Type                    | Default                          | Notes                                                                |
|------------------|-------------------------|----------------------------------|----------------------------------------------------------------------|
| `mountPath`      | `string`                | `'/status'`                      | Must start with `/`. `/incidents` and `/incidents.xml` mount under it. |
| `name`           | `string`                | required                         | Site name, shown in page title + headings.                          |
| `domain`         | `string`                | required                         | Used in the Atom feed tag URI + display.                            |
| `sources`        | `BananapulseSource[]`   | required                         | v0.1 only renders the first source.                                 |
| `incidentsPath`  | `string`                | `'./src/data/incidents.json'`    | Relative to consumer project root.                                  |
| `themeCssVarMap` | `Record<string,string>` | `{}`                             | Remap Bananapulse CSS vars to consumer-owned tokens.                |
| `themeCssPath`   | `string`                | unset (uses default-theme.css)   | Bring-your-own theme file path (relative to project root).          |

`BananapulseSource`:

```ts
{ url: string; type: 'queue-status' }
```

v0.1 ships ONE upstream mapper, `queue-status`, that expects JSON shaped
around server-pool capacity (active/max units, queue length, dependency
health flags — see `src/lib/ingest.ts` for the expected shape).
Consumers whose upstream looks different add a new `type` value here
and a matching mapper.

## Adding an incident

1. Append a record to your `src/data/incidents.json`:

   ```json
   {
     "id": "2026-05-21-payments-degraded",
     "title": "Payment provider delayed",
     "startedAt": "2026-05-21T15:00:00Z",
     "severity": "degraded",
     "body": "<p>Payment confirmations are running ~5 min behind.</p>"
   }
   ```

2. Add `"resolvedAt": "..."` when it's resolved.
3. Commit, push, rebuild.

`id` must be unique; it's the URL anchor (`/status/incidents#<id>`) and
the Atom `<id>`. `severity` is one of `degraded | major | maintenance`.

## Working on Bananapulse itself

```bash
# install workspace deps, then:
npm run dev      # starts dev/ standalone preview at :4322
npm run build    # builds the preview site
npm run check    # astro check inside dev/
```

The dev/ directory is a thin Astro site that installs the integration
from the workspace root (`bananapulse: 'file:..'`) so edits to
`src/` hot-reload immediately.

## Deployment

See `DEPLOY.md`.

## Why this exists

This is the OSS engine behind status pages for sites that don't want to
rent Atlassian Statuspage by the seat. v0.1 is single-tenant — one
Bananapulse-powered site per consumer deploy. A hosted SaaS variant
with multi-tenancy is on the roadmap once the single-tenant story is
proven in production.
