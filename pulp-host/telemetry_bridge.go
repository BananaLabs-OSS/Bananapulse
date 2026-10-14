package main

import (
	"context"
	"math"
	"net/http"
)

// This edge only lowers authenticated JSON into the declared optional Lua workflow.
func (b *httpBridge) telemetryHTTP(ctx context.Context, event string, r *http.Request, w http.ResponseWriter) error {
	if !b.families.telemetry {
		return disabledBridgeFamily()
	}
	switch event {
	case "bananapulse.telemetry.configure.v1", "bananapulse.telemetry.poll.v1", "bananapulse.telemetry.snapshot.v1", "bananapulse.telemetry.history.v1", "bananapulse.telemetry.import.v1":
	default:
		http.NotFound(w, r)
		return nil
	}
	var request map[string]any
	if err := decodeBridgeJSON(r, &request, false); err != nil {
		return err
	}
	request = telemetryNumbers(request).(map[string]any)
	var result any
	if err := b.client.callRaw(ctx, event, request, &result); err != nil {
		return err
	}
	writeBridgeJSON(w, http.StatusOK, result)
	return nil
}

// JSON timestamps arrive as float64 through the compatibility edge; preserve the
// exact integer wire type expected by owner contracts (within JSON's safe range).
func telemetryNumbers(value any) any {
	switch v := value.(type) {
	case float64:
		if math.Trunc(v) == v && math.Abs(v) <= 9007199254740991 {
			return int64(v)
		}
		return v
	case map[string]any:
		for k, item := range v {
			v[k] = telemetryNumbers(item)
		}
		return v
	case []any:
		for i, item := range v {
			v[i] = telemetryNumbers(item)
		}
		return v
	default:
		return value
	}
}
