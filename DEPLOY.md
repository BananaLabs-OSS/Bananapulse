# Deploying a Bananapulse-powered site

Bananapulse is **pure static**. Your `npm run build` output (`dist/`) is
plain HTML/CSS/JS — no Worker, no SSR, no edge functions. Drop it on
any static host.

## Build

From your consumer project (the one with Bananapulse in
`astro.config.mjs`):

```bash
npm install
npm run build      # produces ./dist
```

`dist/` is everything you need to serve. The status page lives at
`<dist>/<mountPath>/index.html` and the Atom feed at
`<dist>/<mountPath>/incidents.xml`.

## Deploy targets

### Cloudflare Pages

1. Push the repo to GitHub.
2. Dashboard → Workers & Pages → Create → Pages → Connect to git.
3. Build settings:
   - Framework preset: **Astro**
   - Build command: `npm run build`
   - Build output: `dist`
   - Node version: 20
4. No environment variables needed — Bananapulse has no server runtime.

CF auto-detects static-only Astro and does NOT provision a Worker.

### GitHub Pages

```yaml
# .github/workflows/deploy.yml
name: deploy
on: { push: { branches: [main] } }
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with: { node-version: 20 }
      - run: npm ci && npm run build
      - uses: actions/upload-pages-artifact@v3
        with: { path: dist }
  deploy:
    needs: build
    permissions: { pages: write, id-token: write }
    environment: { name: github-pages }
    runs-on: ubuntu-latest
    steps: [{ uses: actions/deploy-pages@v4 }]
```

### Netlify

`netlify.toml`:

```toml
[build]
  command = "npm run build"
  publish = "dist"
```

### Vercel

`vercel.json`:

```json
{ "buildCommand": "npm run build", "outputDirectory": "dist" }
```

### S3 + CloudFront

```bash
aws s3 sync ./dist s3://your-bucket/ --delete
aws cloudfront create-invalidation --distribution-id <id> --paths '/*'
```

Set `index.html` as the default root object. Optionally enable
"redirect requests for an object" so `/status` serves `/status/index.html`.

### nginx

```nginx
server {
  listen 443 ssl http2;
  server_name status.example.com;
  root /var/www/bananapulse-dist;
  index index.html;
  location / { try_files $uri $uri/ $uri.html =404; }
}
```

### Caddy

```caddy
status.example.com {
  root * /var/www/bananapulse-dist
  try_files {path} {path}/ {path}.html
  file_server
}
```

## CORS — required

The browser fetches the upstream status JSON directly. Your upstream
endpoint **must** send `Access-Control-Allow-Origin` covering the
status site's origin. Easiest:

```http
Access-Control-Allow-Origin: *
```

(Status data is public by definition — `*` is fine.)

If your upstream genuinely cannot serve CORS (legacy system, locked-
down infra), proxy it yourself: stand up a tiny edge function on the
same origin as the status page that forwards the upstream response and
adds CORS headers. Out of scope for Bananapulse v0.1.

## Publishing an incident

1. Edit `src/data/incidents.json` (or whatever path you set as
   `incidentsPath`) in your consumer repo.
2. Commit. Push. Your static host rebuilds in ~30 seconds.
3. The incident shows on `/status`, `/status/incidents`, and in
   `/status/incidents.xml`.

## Verifying after deploy

```bash
# Upstream is reachable + CORS works:
curl -sS -H 'Origin: https://status.example.com' \
     https://api.example.com/status | jq .

# Feed is materialised:
curl -sS https://status.example.com/status/incidents.xml | head -20
```

If the page shows "Upstream status endpoint unreachable", the most
common causes are (in order): CORS misconfigured on the upstream;
upstream is 5xx-ing; URL typo in `astro.config.mjs`.
