# Banana Pulse

A self-hosted, multi-brand status page engine. Astro SSR + vanilla Postgres, no vendor lock-in.

Pulse is not a static "paint your uptime green" page — it's a small **quorum engine**. Independent monitoring sources (your own services, Grafana alerts, UptimeRobot, anything that can POST) push observations in; Pulse weighs them (trusted vs untrusted vantages, votes, dead-man TTLs) and decides what the public page says — including opening and resolving incidents automatically, and staying truthful during a total blackout (auto-resolve is blocked when no source is alive).

## What you get

- **Component tree** — org → products → services/hosts, arbitrary depth. You declare status on leaves; containers derive worst-of-subtree and bubble up.
- **Quorum engine** — one trusted source non-ok ⇒ degraded/investigating; two or more vantages agree ⇒ major. Manual declarations always win. Dead-man TTLs make silent probes go stale instead of pinning their last reading.
- **Ingest adapters** — generic `POST /api/v1/ingest` with per-source tokens, plus native Grafana and UptimeRobot webhook adapters (and a free-tier UptimeRobot API poller, since UptimeRobot paywalls webhooks).
- **Multi-brand scopes** — one deployment serves many domains; each Host header lands on its own subtree with its own accent, wordmark and logo. Light/dark.
- **Admin** — magic-link auth, resource-driven CRUD for components, incidents, maintenance, sources and mappings, subscribers.
- **Feeds** — Atom + JSON incident feeds, `summary.json` for embedding status badges in your other sites.

## The seam

Everything company-specific lives in exactly three places:

| File | What it holds |
| --- | --- |
| `src/pulse.config.ts` | company name, scopes (host → brand → root component), support email |
| `src/styles/brand.css` | per-scope accent palette (light + dark) |
| `public/brand/*` | logos |

Everything else is brandless engine. **Run your instance as a downstream git repo**: fork/clone, commit your seam changes on top, and keep this repo as `upstream` — engine updates merge down without touching your branding.

```sh
git remote add upstream https://github.com/BananaLabs-OSS/Bananapulse.git
git pull upstream master   # engine updates; your seam commits ride on top
```

## Quickstart

```sh
npm install
cp .env.example .env                          # fill in DATABASE_URL etc.
npm run db:migrate                            # drizzle migrations → your Postgres
node --env-file=.env scripts/seed-tree.mjs    # starter component tree (edit first)
npm run dev
```

Deploy: the repo ships a Netlify adapter + two scheduled functions (`sweep-cron` drives the dead-man sweep every minute, `uptimerobot-poll` every 5). Neither is load-bearing logic — they only POST to the app's own `/api/v1/sweep` and ingest endpoints, so any scheduler on any host can replace them; swap the Astro adapter to move off Netlify entirely.

`UPTIME-WIRING.md` walks through wiring UptimeRobot end-to-end in plain English.

## License

MIT
