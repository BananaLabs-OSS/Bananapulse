package main

import "testing"

func TestNormalizedAllowlistExcludesRawProviderData(t *testing.T) {
	q := Request{Version: "game-server.v1", Adapter: "game-server/space-engineers", OK: true, Payload: `{"data":{"SimSpeed":1.02,"Players":3,"Secret":"private"}}`, Definitions: []Definition{{ID: "speed", Label: "Speed", Unit: "ratio", Path: "data.SimSpeed"}}}
	out, e := normalize(q)
	if e != nil {
		t.Fatal(e)
	}
	metrics := out["metrics"].([]map[string]any)
	if len(metrics) != 1 || metrics[0]["value"] != 1.02 {
		t.Fatal(metrics)
	}
	if _, ok := out["payload"]; ok {
		t.Fatal("raw provider escaped")
	}
}
func TestMissingWrongTypesAndUnknownAdapterFailClosed(t *testing.T) {
	for _, payload := range []string{`{"data":{}}`, `{"data":{"SimSpeed":"1"}}`, `{"data":{"SimSpeed":true}}`} {
		_, e := normalize(Request{Version: "game-server.v1", Adapter: "game-server/space-engineers", OK: true, Payload: payload, Definitions: []Definition{{Path: "data.SimSpeed", Unit: "ratio"}}})
		if e == nil {
			t.Fatal("accepted invalid metric")
		}
	}
	if _, e := normalize(Request{Version: "game-server.v1", Adapter: "arbitrary"}); e == nil {
		t.Fatal("accepted unknown adapter")
	}
}
func TestFailureCannotTurnProviderPayloadIntoMetrics(t *testing.T) {
	out, e := normalize(Request{Version: "game-server.v1", Adapter: "game-server/steam-query", Payload: `secret`})
	if e != nil || len(out["metrics"].([]map[string]any)) != 0 {
		t.Fatal(out, e)
	}
}
