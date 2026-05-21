# Minimal Bananapulse config

The smallest possible consumer setup. Drops a status page at `/status`,
polls one upstream JSON endpoint every 30 seconds, and renders the
default dark theme.

## 1. Install

```bash
npm install bananapulse
```

## 2. Wire it up

```js
// astro.config.mjs
import { defineConfig } from 'astro/config';
import bananapulse from 'bananapulse';

export default defineConfig({
  integrations: [
    bananapulse({
      name: 'My Service',
      domain: 'example.com',
      sources: [
        { url: 'https://api.example.com/status', type: 'queue-status' },
      ],
    }),
  ],
});
```

## 3. (Optional) Add incidents

Create `src/data/incidents.json` in your project:

```json
[
  {
    "id": "2026-05-21-payments-degraded",
    "title": "Payment provider delayed",
    "startedAt": "2026-05-21T15:00:00Z",
    "severity": "degraded",
    "body": "<p>Payment confirmations are running ~5 min behind.</p>"
  }
]
```

Add `"resolvedAt": "..."` to mark it resolved. Commit + push.

## 4. Build + deploy

```bash
npm run build
```

`dist/` is fully static. Upload it anywhere. See `DEPLOY.md` for host-
specific notes.
