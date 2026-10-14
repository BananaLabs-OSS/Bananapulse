# Pulp game-server adapter package

`bananalabs/game-server@1.0.0` is a capability-free Wasm cell providing `game-server.v1.normalize`. The native protocol adapters collect Steam A2S or signed Space Engineers JSON. This cell translates only configured finite numeric/boolean fields into the generic telemetry contract. Raw provider payloads and unknown fields are never retained or rendered. Unknown adapter IDs and missing/wrong-type metrics fail closed.

Game-specific field defaults and native protocol implementations live under `modules/telemetry/game_server/adapters`. The generic telemetry package owns state and policy; Lua composes it with this adapter package and the existing monitor/notification owners. HTTPS JSON collection is also supported through the explicit `http-json` adapter identity.

See `../pulp-telemetry/README.md` for packaging and activation. This package has no storage or network capabilities and cannot create incidents or deliver notifications itself.
