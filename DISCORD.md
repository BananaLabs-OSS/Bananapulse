# Discord notifications

Open **Admin → Notifications**, paste an incoming webhook copied from Discord channel settings, enable alerts, and save. Blank URL retains the current secret. The saved URL is never returned to the browser. Use **Send test** to send an explicit test message; remove the webhook to disable and cancel pending Discord alerts.

This feature requires the Pulp notification composition and an updated `pulp-host` and `subscription-outbox` Wasm artifact. Enable `PULP_BRIDGE_ENABLE_AUTH_ADMIN=true` with a nonempty private bridge token. The web frontend authenticates the admin session and requires same-origin JSON mutations. The bridge must remain private and the host needs HTTPS egress to `discord.com`. For the Node Docker frontend, pass `SITE_URL` as a build argument as well as a runtime variable so the trusted public hostname and HTTPS origin are compiled into the adapter. Legacy Postgres-only deployments display an unavailable message rather than silently discarding settings.

The subscriber owner persists nonsecret event preferences and queues only committed `incident.opened` and `incident.resolved` transitions. Retries of the same monitor command do not queue duplicate events. Existing email delivery claims exclude Discord intents. Disabling a channel or event cancels its pending intents.

The host keeps the webhook encrypted with AES-GCM in `PULP_STORAGE_ROOT/discord-webhook.sqlite` and its random key in `discord-webhook.key`, created with mode 0600. Back up both with the persistent owner data. The URL is restricted to Discord's HTTPS incoming-webhook endpoint; redirects are rejected. No credential is sent through Lua or included in public projections, outbox recipients, logs, or admin responses.

The host drains pending Discord messages with the existing durable outbox worker, honors Discord rate limits, and retries transport/5xx failures. Successful delivery records a durable receipt; permanent 4xx failures record failure. Discord webhooks do not provide provider idempotency: an ambiguous transport failure after Discord accepted a message can produce a duplicate on retry. Test messages are sent only on explicit admin action and are throttled.

Messages contain an incident title, public incident link, and recovery duration. Raw monitor details and target addresses are omitted, and automatic mentions are disabled. No real Discord webhook is required for tests; integration tests run the real Lua/Wasm owners and a recording HTTP transport.
