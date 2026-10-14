package main

import (
	"encoding/json"
	"errors"
	"github.com/vmihailenco/msgpack/v5"
	"math"
	"strings"
)

type Definition struct {
	ID    string `json:"id" msgpack:"id"`
	Label string `json:"label" msgpack:"label"`
	Unit  string `json:"unit" msgpack:"unit"`
	Path  string `json:"path" msgpack:"path"`
}
type Request struct {
	Version     string       `msgpack:"version"`
	Adapter     string       `msgpack:"adapter"`
	ModuleID    string       `msgpack:"moduleId"`
	ComponentID string       `msgpack:"componentId"`
	At          int64        `msgpack:"observedAt"`
	OK          bool         `msgpack:"ok"`
	Publish     bool         `msgpack:"publish"`
	Payload     string       `msgpack:"payload"`
	Definitions []Definition `msgpack:"metrics"`
}

func normalize(q Request) (map[string]any, error) {
	if q.Version != "game-server.v1" || (q.Adapter != "game-server/space-engineers" && q.Adapter != "game-server/steam-query" && q.Adapter != "http-json") || len(q.Payload) > 65536 || len(q.Definitions) > 32 {
		return nil, errors.New("invalid game adapter request")
	}
	metrics := []map[string]any{}
	if q.OK {
		var payload any
		if err := json.Unmarshal([]byte(q.Payload), &payload); err != nil {
			return nil, errors.New("invalid provider JSON")
		}
		if len(q.Definitions) == 0 {
			return nil, errors.New("metric definitions required")
		}
		for _, d := range q.Definitions {
			var value any = payload
			for _, part := range strings.Split(d.Path, ".") {
				object, ok := value.(map[string]any)
				if !ok {
					return nil, errors.New("missing metric")
				}
				value, ok = object[part]
				if !ok {
					return nil, errors.New("missing metric")
				}
			}
			if flag, ok := value.(bool); ok {
				if d.Unit != "boolean" {
					return nil, errors.New("unexpected boolean metric")
				}
				if flag {
					value = float64(1)
				} else {
					value = float64(0)
				}
			}
			numeric, ok := value.(float64)
			if !ok || math.IsNaN(numeric) || math.IsInf(numeric, 0) {
				return nil, errors.New("invalid numeric metric")
			}
			metrics = append(metrics, map[string]any{"id": d.ID, "label": d.Label, "unit": d.Unit, "value": numeric})
		}
	}
	return map[string]any{"version": "telemetry.v1", "moduleId": q.ModuleID, "componentId": q.ComponentID, "observedAt": q.At, "ok": q.OK, "publish": q.Publish, "metrics": metrics}, nil
}
func normalizeWire(raw []byte) ([]byte, error) {
	var q Request
	if err := msgpack.Unmarshal(raw, &q); err != nil {
		return nil, err
	}
	out, err := normalize(q)
	if err != nil {
		return nil, err
	}
	return msgpack.Marshal(out)
}
