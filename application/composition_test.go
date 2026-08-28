package application

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestApplicationManifestAndLuaDigest(t *testing.T) {
	app := readFile(t, "pulp.app.toml")
	if got := tomlString(app, "name"); got != "bananapulse" {
		t.Fatalf("application name = %q, want bananapulse", got)
	}
	if got := tomlBool(app, "require_wasm_sha256"); !got {
		t.Fatal("application must require exact WASM digests")
	}
	wantCells := []string{
		"../pulp-monitor/pulp.cell.toml",
		"../pulp-subscribers/pulp.cell.toml",
		"../pulp-auth/pulp.cell.toml",
		"lua-orchestrator.cell.toml",
	}
	if got := tomlArray(app, "cells"); !sameStrings(got, wantCells) {
		t.Fatalf("application cells = %#v, want %#v", got, wantCells)
	}
	for _, path := range wantCells {
		if filepath.IsAbs(path) {
			t.Fatalf("cell path must be application-relative: %q", path)
		}
		if _, err := os.Stat(filepath.Clean(path)); err != nil {
			t.Fatalf("cell manifest %q: %v", path, err)
		}
		cell := parseCell(t, path)
		wasmPath := filepath.Join(filepath.Dir(path), cell.wasm)
		wasm, err := os.ReadFile(filepath.Clean(wasmPath))
		if err != nil {
			t.Fatalf("read cell artifact %q: %v", wasmPath, err)
		}
		digest := sha256.Sum256(wasm)
		if cell.wasmSHA256 != hex.EncodeToString(digest[:]) {
			t.Fatalf("%s wasm digest = %q, want %x", path, cell.wasmSHA256, digest)
		}
	}
	if got := tomlTableString(app, "orchestrator", "manifest"); got != "lua-orchestrator.cell.toml" {
		t.Fatalf("orchestrator manifest = %q", got)
	}
	if got := tomlTableString(app, "orchestrator", "script"); got != "bananapulse.lua" {
		t.Fatalf("orchestrator script = %q", got)
	}
	script := []byte(readFile(t, "bananapulse.lua"))
	digest := sha256.Sum256(script)
	if got := tomlTableString(app, "orchestrator", "sha256"); got != hex.EncodeToString(digest[:]) {
		t.Fatalf("orchestrator digest = %q, want %x", got, digest)
	}
}

func TestCompositionProviderDAGAndHostBoundary(t *testing.T) {
	manifestPaths := []string{
		"../pulp-monitor/pulp.cell.toml",
		"../pulp-subscribers/pulp.cell.toml",
		"../pulp-auth/pulp.cell.toml",
		"lua-orchestrator.cell.toml",
	}
	cells := map[string]cellManifest{}
	providers := map[string][]string{}
	for _, path := range manifestPaths {
		cell := parseCell(t, path)
		if _, duplicate := cells[cell.name]; duplicate {
			t.Fatalf("duplicate cell name %q", cell.name)
		}
		cells[cell.name] = cell
		for _, provider := range cell.provides {
			providers[provider] = append(providers[provider], cell.name)
		}
	}

	lua := cells["lua-orchestrator"]
	if len(lua.capabilities) != 0 {
		t.Fatalf("Lua capabilities = %#v, want none", lua.capabilities)
	}
	if !sameStrings(lua.dependsOn, []string{"pulp-monitor", "subscription-outbox"}) {
		t.Fatalf("Lua dependencies = %#v", lua.dependsOn)
	}
	for _, capability := range lua.consumes {
		if strings.HasPrefix(capability, "bananapulse.auth.") {
			t.Fatalf("Lua must not consume host-auth provider %q", capability)
		}
	}
	for _, capability := range lua.consumes {
		if got := providers[capability]; len(got) != 1 {
			t.Fatalf("Lua consumes %q with providers %#v, want exactly one", capability, got)
		}
	}
	for _, dependency := range lua.dependsOn {
		if _, ok := cells[dependency]; !ok {
			t.Fatalf("Lua depends on missing cell %q", dependency)
		}
	}

	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		if visiting[name] {
			t.Fatalf("composition dependency cycle includes %q", name)
		}
		if visited[name] {
			return
		}
		visiting[name] = true
		for _, dependency := range cells[name].dependsOn {
			visit(dependency)
		}
		delete(visiting, name)
		visited[name] = true
	}
	for name := range cells {
		visit(name)
	}
}

func TestLuaOwnsSequencingWithoutPrivilegedEffects(t *testing.T) {
	script := readFile(t, "bananapulse.lua")
	for _, event := range []string{
		"bananapulse.monitor.command.v1",
		"bananapulse.monitor.admin.command.v1",
		"bananapulse.monitor.migration.import.v1",
		"bananapulse.monitor.ingest.authenticated.v1",
		"bananapulse.monitor.sweep.v1",
		"bananapulse.monitor.query.v1",
		"bananapulse.monitor.projection.v1",
		"bananapulse.subscriber.subscribe.v1",
		"bananapulse.subscriber.confirm.v1",
		"bananapulse.subscriber.unsubscribe.v1",
		"bananapulse.subscriber.confirmation.resend.v1",
		"bananapulse.subscriber.projection.v1",
		"bananapulse.subscriber.admin.list.v1",
		"bananapulse.subscriber.admin.get.v1",
		"bananapulse.subscriber.admin.delete.v1",
		"bananapulse.subscriber.admin.state.set.v1",
		"bananapulse.subscriber.migration.import.v1",
		"bananapulse.incident.publish.v1",
		"bananapulse.maintenance.publish.v1",
		"bananapulse.host.email.outbox.claim.v1",
		"bananapulse.host.email.outbox.receipt.apply.v1",
	} {
		if !strings.Contains(script, `"`+event+`"`) {
			t.Fatalf("Lua does not register %q", event)
		}
	}
	monitorIndex := strings.Index(script, "local monitor_result = pulp.call_raw(")
	transitionIndex := strings.Index(script, "local transition_result = pulp.call_raw(")
	if monitorIndex < 0 || transitionIndex < 0 || monitorIndex >= transitionIndex {
		t.Fatal("transition workflow must commit monitor state before creating notification intents")
	}
	lower := strings.ToLower(script)
	for _, forbidden := range []string{
		"resend.com",
		"smtp",
		"sendgrid",
		"os.execute",
		"io.open",
		"storage.sqlite",
		"transport.http",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("Lua contains privileged implementation %q", forbidden)
		}
	}
	if !strings.Contains(script, "providers.outbox_claim") ||
		!strings.Contains(script, "providers.outbox_receipt_apply") {
		t.Fatal("email host boundary must claim intents and apply durable receipts")
	}
	if !strings.Contains(script, "providers.transition_apply") {
		t.Fatal("committed monitor transitions must drive subscriber intent creation")
	}
}

func TestSecondWaveRoutingAndSecretBoundaries(t *testing.T) {
	script := readFile(t, "bananapulse.lua")
	for event, provider := range map[string]string{
		"bananapulse.monitor.migration.import.v1":       "providers.monitor_command",
		"bananapulse.subscriber.confirmation.resend.v1": "providers.subscriber_confirmation_resend",
		"bananapulse.subscriber.admin.list.v1":          "providers.subscriber_admin_list",
		"bananapulse.subscriber.admin.get.v1":           "providers.subscriber_admin_get",
		"bananapulse.subscriber.admin.delete.v1":        "providers.subscriber_admin_delete",
		"bananapulse.subscriber.admin.state.set.v1":     "providers.subscriber_admin_state_set",
		"bananapulse.subscriber.migration.import.v1":    "providers.subscriber_migration_import",
	} {
		pattern := regexp.MustCompile(
			`(?s)pulp\.on\(\s*"` + regexp.QuoteMeta(event) +
				`"\s*,\s*forward\([^,]+,\s*` + regexp.QuoteMeta(provider) + `\s*\)\s*\)`,
		)
		if !pattern.MatchString(script) {
			t.Fatalf("Lua event %q does not forward through %s", event, provider)
		}
	}
	for event, requestField := range map[string]string{
		"bananapulse.monitor.admin.command.v1":        "request_msgpack",
		"bananapulse.monitor.ingest.authenticated.v1": "request_msgpack",
		"bananapulse.monitor.sweep.v1":                "request_msgpack",
		"bananapulse.incident.publish.v1":             "monitor_request_msgpack",
		"bananapulse.maintenance.publish.v1":          "monitor_request_msgpack",
	} {
		pattern := regexp.MustCompile(
			`(?s)pulp\.on\(\s*"` + regexp.QuoteMeta(event) +
				`"\s*,\s*commit_transitions\(\s*"` + regexp.QuoteMeta(requestField) + `"\s*\)\s*\)`,
		)
		if !pattern.MatchString(script) {
			t.Fatalf("Lua event %q does not commit and apply transitions from %q", event, requestField)
		}
	}

	// Replay/import is a state restoration boundary. It must never enter the
	// state-commit-plus-notification helper used by live incident publishing.
	for _, event := range []string{
		"bananapulse.monitor.migration.import.v1",
		"bananapulse.subscriber.migration.import.v1",
	} {
		if regexp.MustCompile(
			`(?s)pulp\.on\(\s*"` + regexp.QuoteMeta(event) + `"\s*,\s*commit_transitions\(`,
		).MatchString(script) {
			t.Fatalf("migration event %q must never publish notifications", event)
		}
	}

	// Authentication providers carry credential material and are invoked only
	// by the privileged host. Lua receives authenticated assertions on the
	// monitor/admin application events, never tokens or hashes.
	manifest := readFile(t, "lua-orchestrator.cell.toml")
	for _, text := range []string{script, manifest} {
		lower := strings.ToLower(text)
		for _, forbidden := range []string{
			"bananapulse.auth.",
			"magic-link",
			"session.validate",
			"api-token",
			"credential_hash",
			"token_hash",
		} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("Lua boundary contains host-auth provider or secret field %q", forbidden)
			}
		}
	}
}

type cellManifest struct {
	name         string
	wasm         string
	wasmSHA256   string
	provides     []string
	consumes     []string
	dependsOn    []string
	capabilities []string
}

func parseCell(t *testing.T, path string) cellManifest {
	t.Helper()
	text := readFile(t, path)
	cell := cellManifest{
		name:         tomlString(text, "name"),
		wasm:         tomlString(text, "wasm"),
		wasmSHA256:   tomlString(text, "wasm_sha256"),
		provides:     tomlArray(text, "provides"),
		consumes:     tomlArray(text, "consumes"),
		dependsOn:    tomlArray(text, "depends_on"),
		capabilities: tomlArray(text, "capabilities"),
	}
	if cell.name == "" || cell.wasm == "" || cell.wasmSHA256 == "" {
		t.Fatalf("cell manifest %q is missing name, wasm, or wasm_sha256", path)
	}
	return cell
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func tomlString(text, key string) string {
	match := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `\s*=\s*"([^"]*)"\s*$`).FindStringSubmatch(text)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func tomlBool(text, key string) bool {
	match := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `\s*=\s*(true|false)\s*$`).FindStringSubmatch(text)
	return len(match) == 2 && match[1] == "true"
}

func tomlTableString(text, table, key string) string {
	match := regexp.MustCompile(`(?ms)\[` + regexp.QuoteMeta(table) + `\](.*?)(?:^\[|\z)`).FindStringSubmatch(text)
	if len(match) != 2 {
		return ""
	}
	return tomlString(match[1], key)
}

func tomlArray(text, key string) []string {
	match := regexp.MustCompile(`(?ms)^` + regexp.QuoteMeta(key) + `\s*=\s*\[(.*?)\]`).FindStringSubmatch(text)
	if len(match) != 2 {
		return nil
	}
	values := regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(match[1], -1)
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value[1])
	}
	return result
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
