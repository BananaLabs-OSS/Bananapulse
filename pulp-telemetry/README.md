# Pulp telemetry package

`bananalabs/telemetry@1.0.0` is an executable Wasm cell, not a Python package declaration. It owns allowlisted numeric history, freshness, persistent threshold dwell and a durable queue of monitor observations. Its only host capability is `storage.sqlite`; it cannot access provider credentials, make network requests or send notifications.

Providers are versioned `telemetry.v1.*`: configure, ingest, snapshot, history, pending, ack and fenced legacy import. Configuration is immutable per module identity. Missing collection or an unavailable availability gate resets dwell and discards pending performance claims; it never supplies a fabricated recovery. Late/replayed samples cannot replace current state. Queries return bounded recent history. Failed downstream commits retain stable observation IDs for retry.

Lua in the optional application composition normalizes an adapter report, commits telemetry state, sends pending observations to the existing monitor, passes the committed monitor result to the notification owner, then acknowledges delivery. Default BananaPulse composition remains unchanged. The private bridge requires `PULP_BRIDGE_ENABLE_TELEMETRY=true` and the opt-in `application/pulp.telemetry.app.toml` manifest.

The native transport edge still holds credentials and performs read-only provider queries. It has no metric database or performance policy state in Pulp mode. `modules/telemetry/collector.py` defaults to Pulp ownership; `owner: legacy` is retained only for rollback. The independent game availability/ICMP probes remain transport processes; their existing availability history is owned by Pulp's monitor.

Build and package with `scripts/build-telemetry.sh`. Go1.25.13 and `pulp-registry` are required. Both `pulp.cell.toml` and `pulp.module.toml` pin generated artifacts. `application/telemetry.pulp.lock` locks the two packages by manifest and artifact digest. Bump the cell and module versions before publishing changed bytes under a new immutable package release. No production registry credentials are required for local package verification.

`go test ./...` tests durable policy/state, replay, gaps, invalid metrics and migration fencing. The host suite tests the real Lua/Wasm composition through authenticated HTTP, including failed polls, performance incidents and duplicate delivery.
