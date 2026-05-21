# Sessions-style Bananapulse config

A more complete setup that:

- Mounts the status page at `/status`
- Reads incidents from a non-default path
- Remaps Bananapulse's CSS vars to the consumer's existing design tokens
  (so the status page inherits the host site's brand without forking)

```js
// astro.config.mjs
import { defineConfig } from 'astro/config';
import bananapulse from 'bananapulse';

export default defineConfig({
  integrations: [
    bananapulse({
      mountPath: '/status',
      name: 'Sessions',
      domain: 'sessions.gg',
      sources: [
        { url: 'https://api.sessions.gg/status', type: 'queue-status' },
      ],
      incidentsPath: './src/data/status-incidents.json',
      themeCssVarMap: {
        // Bananapulse var → host-site var
        '--accent':        '--brand-yellow',
        '--accent-hover':  '--brand-yellow-hover',
        '--bg':            '--surface-0',
        '--card':          '--surface-1',
        '--tx':            '--text-primary',
        '--muted':         '--text-secondary',
        '--dim':           '--text-tertiary',
        '--border':        '--border-subtle',
      },
    }),
  ],
});
```

## Notes

- `themeCssVarMap` keys are the Bananapulse vars defined in
  `default-theme.css`; values are the consumer's own custom-property
  names. Bananapulse emits a one-time `:root { --accent: var(--brand-yellow); ... }`
  block in `<head>`, so the host site's tokens win.
- The host site is responsible for *defining* those custom properties on
  `:root` (or an ancestor of `/status`).
- If you want a fully bespoke theme, pass `themeCssPath: './src/styles/my-status-theme.css'`
  to skip Bananapulse's default-theme.css entirely.
- The upstream at `https://api.sessions.gg/status` must serve
  `Access-Control-Allow-Origin: https://sessions.gg` (or `*`). The
  browser is doing the fetch, not a server.
