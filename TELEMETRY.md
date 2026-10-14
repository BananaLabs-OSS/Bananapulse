# Optional telemetry modules

Bananapulse remains a generic status engine. Collectors own authentication and field mapping; instance configuration owns metric selection and alert thresholds; existing monitor observations/quorum/incidents and the notification outbox own status and delivery.

`modules/telemetry/collector.py` is an optional standard-library sidecar for HTTPS JSON sources. It supports unauthenticated JSON and VRage HMAC signing. Its config declares numeric metric IDs, labels, units and JSON paths. A different integration can implement the same `telemetry.v1` snapshot contract without adding game-specific fields to the engine.

## Collection and storage

Set `TELEMETRY_CONFIG`, `TELEMETRY_DB`, `PULP_BRIDGE_URL` and `PULP_BRIDGE_TOKEN`; supply the provider credential through the configured `keyEnv` (VRage uses the raw `VRAGE_KEY`). Credentials do not belong in JSON configs. The collector only makes GET requests to the provider, rejects redirects and bounds responses. Only allowlisted finite numeric/boolean fields are stored in its private SQLite database. History retention defaults to seven days; samples survive restarts. `/history` returns up to 2,000 recent samples from the last day, newest retained samples first in chronological presentation.

The loopback HTTP service defaults to port 8790. `/snapshot` and `/history` require `Authorization: Bearer <PULP_BRIDGE_TOKEN>`. No host port should be published. The module is optional: default installs neither start a collector nor fetch its snapshots.

## Independent controls

1. Collection starts only when the operator runs the sidecar.
2. Status/notifications require `policy.enabled: true`.
3. Public numeric display requires an explicit `PULSE_TELEMETRY_MODULES` configuration.

For example, to expose only simulation speed and player count:

```json
[{"id":"vrage","componentId":"game-performance","url":"http://127.0.0.1:8790/snapshot","enabled":true,"publicMetrics":["simulation_speed","players"]}]
```

Put this JSON in the frontend `PULSE_TELEMETRY_MODULES` environment variable. The generic collapsed Service details panel appears only within the configured component's subtree. All other fields remain private; stale samples, failed polls and identity mismatches show unavailable. Optional module failures do not replace the site's main status or break page rendering. No arbitrary provider response is rendered publicly.

## Alert policy

The example VRage config leaves alerts disabled. SE-PULSE enables a configurable policy: simulation speed below 0.8 for 90 seconds declares degraded; speed at least 0.95 for 120 seconds recovers. These are starting operator thresholds, not game vendor guarantees. Values between thresholds retain the current state. Server readiness must be true. Collection gaps reset dwell timers; a failed API request never creates an OK observation or a game-down observation.

Use a dedicated performance component and source: a game-port observation on the same component could otherwise satisfy recovery while the performance source is stale. Current monitor reconciliation requires live evidence to resolve an automatic incident. Policy state and pending observation deliveries are persisted atomically with samples; delivery retries retain command IDs, expired deliveries are discarded, and pending deliveries are bounded by the reading TTL. Existing incident transitions produce the existing Discord notifications; minor incidents are described as degradation rather than server unavailability. Public metric display is independent of these notifications.

## Validation

`python3 -m unittest discover -s modules/telemetry` verifies numeric field privacy, missing/invalid metrics, storage restart/expiry and policy hysteresis/gaps. `npm test`, `npm run check` and `npm run build:local` cover the optional display and frontend. `node scripts/test-observation-queries.mjs` uses an isolated PostgreSQL container to verify the observation-query backport against long histories and missing source/component combinations.
