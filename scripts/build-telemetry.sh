#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
registry_bin="${PULP_REGISTRY_BIN:-pulp-registry}"
package_registry="$(mktemp -d)"
trap 'rm -rf "$package_registry"' EXIT
cd "$repo_root"
for cell in pulp-telemetry pulp-game-server; do
  (cd "$cell" && GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -buildvcs=false -buildmode=c-shared -trimpath -o "$cell.wasm" .)
  "$registry_bin" refresh-cell -manifest "$cell/pulp.cell.toml"
  "$registry_bin" refresh -manifest "$cell/pulp.module.toml" -blob "wasm32-wasip1+pulp-v1=$cell/$cell.wasm"
  "$registry_bin" publish -registry "$package_registry" -manifest "$cell/pulp.module.toml" -blob "wasm32-wasip1+pulp-v1=$cell/$cell.wasm"
done
"$registry_bin" refresh-app -manifest application/pulp.telemetry.app.toml
"$registry_bin" resolve -registry "$package_registry" -target wasm32-wasip1+pulp-v1 -root "bananalabs/telemetry@$(sed -n 's/^version = "\([^"]*\)"/\1/p' pulp-telemetry/pulp.module.toml)" -root "bananalabs/game-server@$(sed -n 's/^version = "\([^"]*\)"/\1/p' pulp-game-server/pulp.module.toml)" -out application/telemetry.pulp.lock
"$registry_bin" verify -registry "$package_registry" -lock application/telemetry.pulp.lock
