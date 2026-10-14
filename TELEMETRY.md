# Optional telemetry modules

Bananapulse remains a generic status engine. Collectors own authentication and field mapping; instance configuration owns metric selection and alert thresholds; existing monitor observations/quorum/incidents and the notification outbox own status and delivery.

`pulp-telemetry` and `pulp-game-server` are executable Pulp packages with Wasm artifacts, cell/module manifests and a digest lock. `modules/telemetry/collector.py` is their optional native network edge. Its provider registry supports generic HTTPS JSON and an optional game-server module with protocol-specific adapters. Its config declares numeric metric IDs, labels, units and JSON paths. A different integration can implement the same `telemetry.v1` snapshot contract without adding game-specific fields to the engine.

## Collection and storage

Set `TELEMETRY_CONFIG`, `PULP_BRIDGE_URL` and `PULP_BRIDGE_TOKEN` (`TELEMETRY_DB` applies only to legacy mode); supply the provider credential through the configured `keyEnv` (VRage uses the raw `VRAGE_KEY`). Credentials do not belong in JSON configs. The HTTPS adapter only makes GET requests to the provider, rejects redirects and bounds responses. Only allowlisted finite numeric/boolean fields are stored by the Pulp telemetry owner through its `storage.sqlite` capability. History retention defaults to seven days; samples and alert state survive cell/host restarts. The native edge has no database in Pulp mode. `/history` returns up to 2,000 recent samples from the last day, also bounded to a 512 KiB payload, newest retained samples first in chronological presentation.

The loopback HTTP service defaults to port 8790. `/snapshot` and `/history` require `Authorization: Bearer <PULP_BRIDGE_TOKEN>`. No host port should be published. The module is optional: default installs neither start a collector nor fetch its snapshots.

## Independent controls

1. Collection starts only when the operator runs the sidecar.
2. Status/notifications require `policy.enabled: true`.
3. Public numeric display requires an explicit `PULSE_TELEMETRY_MODULES` configuration.

For example, to expose only simulation speed and player count:

```json
[{"id":"vrage","componentId":"game-performance","url":"http://127.0.0.1:8790/snapshot","enabled":true,"publicMetrics":["simulation_speed","players"]}]
```

Put this JSON in the frontend `PULSE_TELEMETRY_MODULES` environment variable. The generic open Live stats panel appears within the configured component's subtree (or `displayComponentId` subtree when attached to another public service). All other fields remain private; stale samples, failed polls and identity mismatches show unavailable. Optional module failures do not replace the site's main status or break page rendering. No arbitrary provider response is rendered publicly.

## Alert policy

The example VRage config leaves alerts disabled. SE-PULSE enables a configurable policy: simulation speed below 0.8 for 90 seconds declares degraded; speed at least 0.95 for 120 seconds recovers. These are starting operator thresholds, not game vendor guarantees. Values between thresholds retain the current state. Server readiness must be true. Collection gaps reset dwell timers; a failed API request never creates an OK observation or a game-down observation.

Use a dedicated performance component and source: a game-port observation on the same component could otherwise satisfy recovery while the performance source is stale. Current monitor reconciliation requires live evidence to resolve an automatic incident. Policy state and pending observation deliveries are persisted atomically with samples; delivery retries retain command IDs, expired deliveries are discarded, and pending deliveries are bounded by the reading TTL. Existing incident transitions produce the existing Discord notifications; minor incidents are described as degradation rather than server unavailability. Public metric display is independent of these notifications.

## Validation

`python3 -m unittest discover -s modules/telemetry` verifies numeric field privacy, missing/invalid metrics, storage restart/expiry and policy hysteresis/gaps. `npm test`, `npm run check` and `npm run build:local` cover the optional display and frontend. `node scripts/test-observation-queries.mjs` uses an isolated PostgreSQL container to verify the observation-query backport against long histories and missing source/component combinations.

Rules may also use `direction: "above"`, `above`, and `recoverBelow` for latency-style metrics. Optional `gateComponentId` and `gateSourceId` require a fresh responding availability observation before publishing performance status. Private numeric collection continues during availability failures, while alert dwell timers reset and no competing performance declaration is emitted.


## Module and adapter boundary

The Pulp telemetry cell owns numeric validation, SQLite retention, freshness, persistent alert dwell and durable observation receipts. Lua commits observations and notification transitions; the native edge serves authenticated snapshot proxies. It delegates collection to an explicit provider registry. `game_server/` is the game-server module; its `adapters/space_engineers.py` owns VRage signing and server-info field defaults, while `adapters/steam_query.py` owns Steam A2S_INFO and query metric defaults. Neither adapter creates incidents or sends notifications. Instance configuration selects service identity, thresholds and presentation.

Select `adapter: "game-server/space-engineers"`, `adapter: "game-server/steam-query"`, or `adapter: "http-json"`. Game adapters supply allowlisted metric defaults; `metrics` can override those defaults. See `vrage.example.json` and `steam-query.example.json`. Existing configs using `auth: "vrage-hmac-sha1"` keep their identity and fields and select the VRage adapter automatically. Unknown adapters fail closed; configuration cannot load arbitrary Python files. Adding a game means registering its adapter under the game-server module, without changing history, incident or notification owners.

Deploy the entire `modules/telemetry` directory, rather than copying `collector.py` alone. Run `python modules/telemetry/collector.py` with the existing environment variables. Separate adapter instances may share a public service through site composition, but must use separate SQLite files and snapshot ports. A failed adapter poll means unavailable telemetry; the existing availability probe remains responsible for declaring game downtime. Steam split/compressed responses are unsupported and fail collection explicitly. RAM/Prometheus collection is not implemented yet.

Run `python3 -m unittest discover -s modules/telemetry` for adapter dispatch, exact VRage signatures, URL checks, allowlist normalization, policy and persistence tests.

## Pulp activation and migration

Use `application/pulp.telemetry.app.toml` instead of the default application manifest and set `PULP_BRIDGE_ENABLE_TELEMETRY=true` on the private host. The six-cell opt-in composition adds a capability-free game adapter cell and a SQLite-only telemetry cell. Default deployments still use the original four-cell manifest and disable the telemetry bridge family. Package identities are `bananalabs/telemetry@1.0.0` and `bananalabs/game-server@1.0.0`; see their READMEs and `application/telemetry.pulp.lock`. `scripts/build-telemetry.sh` rebuilds Wasm, refreshes manifest digests and verifies a newly resolved local package lock.

The collector defaults to `owner: pulp`. Its credentials stay at the native transport boundary; normalization, metric history, freshness and threshold state run in Wasm. Before cutover, stop the old collector after its pending queue drains, back up its database, then run `modules/telemetry/migrate_to_pulp.py <legacy-sqlite-path>` with the configured private bridge environment. The importer reads the old database without modifying it, transfers bounded batches and preserves policy/poll state. Import closes after the first live poll or explicit finish. Keep the old store and `owner: legacy` path for rollback.

SE-PULSE query latency uses a separate private performance component and source, while the native probe commits game availability first. A failed stats module therefore cannot suppress availability reports or make an unavailable game look recovered. Query latency and simulation performance both have durable Pulp state; the instance public projection folds their fresh declarations into one visible server. `evaluationOnly: true` remains available for integrations that need a policy result without publishing a competing observation.

Pulp-owned snapshots can be selected with `transport: "pulp"` instead of `url`; the frontend queries the authenticated `PULP_BRIDGE_URL` snapshot event directly. Snapshot identity is still checked against the original module and component IDs. `displayComponentId` changes presentation only. Both transports enforce freshness, field allowlists, a two-second deadline and a 16 KiB response limit. Counts, ratios and readiness have readable formats; reported load is never presented as host CPU percentage.
