package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/BananaLabs-OSS/Pulp/run"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestOptionalPulpTelemetryRealWasmAndPrivateBridge(t *testing.T) {
	observer := &providerObserver{ready: make(chan run.ApplicationProviderAccess, 1)}
	runtime, err := run.NewDirectApplicationRuntime(filepath.Join("..", "application", "pulp.telemetry.app.toml"), run.DirectApplicationOptions{StorageRoot: t.TempDir(), Lifecycle: observer})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err = runtime.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(context.Background())
	client, err := newApplicationClient(<-observer.ready)
	if err != nil {
		t.Fatal(err)
	}
	if err := configureSubscriberDelivery(ctx, client, "https://status.example.test/unsubscribe"); err != nil {
		t.Fatal(err)
	}
	bridge, err := newHTTPBridgeWithFamilies(client, "test-only-token", bridgeFamilies{telemetry: true})
	if err != nil {
		t.Fatal(err)
	}
	call := func(event string, body any, status int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPost, bridgeEventsPath+event, bytes.NewReader(raw))
		r.Header.Set(bridgeTokenHeader, "test-only-token")
		w := httptest.NewRecorder()
		bridge.Handler().ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s HTTP%d: %s", event, w.Code, w.Body.String())
		}
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	now := time.Now().Unix()
	call("bananapulse.telemetry.configure.v1", map[string]any{"version": "telemetry.v1", "id": "test", "componentId": "test-game", "ttlSeconds": 120}, 200)
	fields := []map[string]any{{"id": "speed", "label": "Simulation speed", "unit": "ratio", "path": "data.SimSpeed"}}
	call("bananapulse.telemetry.poll.v1", map[string]any{"version": "game-server.v1", "adapter": "game-server/space-engineers", "moduleId": "test", "componentId": "test-game", "observedAt": now, "ok": true, "publish": false, "payload": `{"data":{"SimSpeed":1.02,"Secret":"never-retained"}}`, "metrics": fields}, 200)
	view := call("bananapulse.telemetry.snapshot.v1", map[string]any{"version": "telemetry.v1", "moduleId": "test", "now": now}, 200)
	if view["observedAt"] != float64(now) || view["lastPollOk"] != true || view["stale"] != false {
		t.Fatal(view)
	}
	metrics := view["metrics"].([]any)
	if len(metrics) != 1 || metrics[0].(map[string]any)["value"] != 1.02 {
		t.Fatal(view)
	}
	call("bananapulse.telemetry.poll.v1", map[string]any{"version": "game-server.v1", "adapter": "game-server/space-engineers", "moduleId": "test", "componentId": "test-game", "observedAt": now + 1, "ok": false, "publish": false}, 200)
	view = call("bananapulse.telemetry.snapshot.v1", map[string]any{"version": "telemetry.v1", "moduleId": "test", "now": now + 1}, 200)
	if view["lastPollOk"] != false || view["observedAt"] != float64(now) {
		t.Fatal(view)
	}
	call("bananapulse.telemetry.history.v1", map[string]any{"version": "telemetry.v1", "moduleId": "test", "now": now + 1}, 200)

	var history struct {
		Samples []struct {
			BucketSeconds int64 `msgpack:"bucketSeconds"`
			Metrics       []struct {
				Value   float64 `msgpack:"value"`
				Samples int64   `msgpack:"samples"`
				Min     float64 `msgpack:"min"`
				Max     float64 `msgpack:"max"`
			} `msgpack:"metrics"`
		} `msgpack:"samples"`
	}
	if err := client.callRaw(ctx, "bananapulse.telemetry.history.v1", map[string]any{"version": "telemetry.v1", "moduleId": "test", "now": now + 1, "since": now - 7*86400, "bucketSeconds": 900}, &history); err != nil {
		t.Fatal(err)
	}
	if len(history.Samples) != 1 || history.Samples[0].BucketSeconds != 900 {
		t.Fatal(history)
	}
	summary := history.Samples[0].Metrics[0]
	if summary.Samples != 1 || summary.Min != 1.02 || summary.Max != 1.02 || summary.Value != 1.02 {
		t.Fatal(summary)
	}
	bridge.families.monitorAdmin = true
	call(eventMonitorAdminCommand, map[string]any{"version": "monitor.v1", "id": "telemetry-test-component", "kind": "upsert_component", "at_unix": now, "component": map[string]any{"id": "lag-service", "name": "Lag service", "kind": "service", "launched": true, "launched_set": true, "fallback_status": "operational"}}, 200)
	call(eventMonitorAdminCommand, map[string]any{"version": "monitor.v1", "id": "telemetry-test-source", "kind": "upsert_source", "at_unix": now, "source": map[string]any{"id": "lag-source", "name": "Lag source", "kind": "push", "trusted": true, "weight": 1, "direct_targets": true, "default_ttl_seconds": 120}}, 200)
	call("bananapulse.telemetry.configure.v1", map[string]any{"version": "telemetry.v1", "id": "lag", "componentId": "lag-service", "policy": map[string]any{"enabled": true, "metricId": "speed", "below": 0.8, "recoverAbove": 0.95, "badForSeconds": 0, "recoveryForSeconds": 0, "sourceId": "lag-source"}}, 200)
	call("bananapulse.telemetry.poll.v1", map[string]any{"version": "game-server.v1", "adapter": "game-server/space-engineers", "moduleId": "lag", "componentId": "lag-service", "observedAt": now, "ok": true, "publish": true, "payload": `{"data":{"SimSpeed":0.7}}`, "metrics": fields}, 200)
	var projection map[string]any
	if err := client.callRaw(ctx, eventMonitorQuery, map[string]any{"version": "monitor.v1", "at_unix": now}, &projection); err != nil {
		t.Fatal(err)
	}
	incidents := projection["incidents"].([]any)
	if len(incidents) != 1 {
		t.Fatalf("performance declaration did not open incident: %#v", projection)
	}
	// Repeating the same poll must not create a second incident or delivery command.
	call("bananapulse.telemetry.poll.v1", map[string]any{"version": "game-server.v1", "adapter": "game-server/space-engineers", "moduleId": "lag", "componentId": "lag-service", "observedAt": now, "ok": true, "publish": true, "payload": `{"data":{"SimSpeed":0.7}}`, "metrics": fields}, 200)
	bridge.families.telemetry = false
	call("bananapulse.telemetry.snapshot.v1", map[string]any{}, 400)
}
