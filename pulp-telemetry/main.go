//go:build wasip1

package main

import (
	"github.com/BananaLabs-OSS/Fiber/pulp"
	"github.com/vmihailenco/msgpack/v5"
	"runtime/debug"
)

func main() {}
func init() {
	pulp.OnInit(func(_ []byte) error {
		debug.SetMemoryLimit(64 << 20)
		o, err := openOwner("")
		if err != nil {
			return err
		}
		pulp.Provide("telemetry.v1.configure", func(raw []byte) ([]byte, error) {
			var c Config
			if err := msgpack.Unmarshal(raw, &c); err != nil {
				return nil, err
			}
			if err := o.configure(c); err != nil {
				return nil, err
			}
			return msgpack.Marshal(map[string]bool{"ok": true})
		})
		pulp.Provide("telemetry.v1.ingest", func(raw []byte) ([]byte, error) {
			var s Sample
			if err := msgpack.Unmarshal(raw, &s); err != nil {
				return nil, err
			}
			if err := o.ingest(s); err != nil {
				return nil, err
			}
			return msgpack.Marshal(map[string]bool{"ok": true})
		})
		pulp.Provide("telemetry.v1.snapshot", func(raw []byte) ([]byte, error) {
			var q Query
			if err := msgpack.Unmarshal(raw, &q); err != nil {
				return nil, err
			}
			out, err := o.snapshot(q)
			if err != nil {
				return nil, err
			}
			return msgpack.Marshal(out)
		})
		pulp.Provide("telemetry.v1.history", func(raw []byte) ([]byte, error) {
			var q Query
			if err := msgpack.Unmarshal(raw, &q); err != nil {
				return nil, err
			}
			out, err := o.history(q)
			if err != nil {
				return nil, err
			}
			return msgpack.Marshal(map[string]any{"version": Version, "samples": out})
		})
		pulp.Provide("telemetry.v1.pending", func(raw []byte) ([]byte, error) {
			var q Query
			if err := msgpack.Unmarshal(raw, &q); err != nil {
				return nil, err
			}
			out, err := o.pending(q)
			if err != nil {
				return nil, err
			}
			return msgpack.Marshal(out)
		})
		pulp.Provide("telemetry.v1.ack", func(raw []byte) ([]byte, error) {
			var a Ack
			if err := msgpack.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			if err := o.ack(a); err != nil {
				return nil, err
			}
			return msgpack.Marshal(map[string]bool{"ok": true})
		})
		pulp.Provide("telemetry.v1.import", func(raw []byte) ([]byte, error) {
			var in Import
			if err := msgpack.Unmarshal(raw, &in); err != nil {
				return nil, err
			}
			if err := o.importHistory(in); err != nil {
				return nil, err
			}
			return msgpack.Marshal(map[string]bool{"ok": true})
		})
		return nil
	})
}
