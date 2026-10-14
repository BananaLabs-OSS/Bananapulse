package main

import (
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"
)

func testOwner(t *testing.T) (*Owner, Config) {
	t.Helper()
	o, e := openOwner(filepath.Join(t.TempDir(), "metrics.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { o.db.Close() })
	c := Config{Version: Version, ID: "vrage", ComponentID: "service", TTL: 120, Retention: 7, Policy: Policy{Enabled: true, MetricID: "speed", ReadyMetric: "ready", Below: .8, RecoverAbove: .95, BadFor: 90, GoodFor: 120, SourceID: "native"}}
	if e = o.configure(c); e != nil {
		t.Fatal(e)
	}
	return o, c
}
func sample(c Config, at int64, speed float64) Sample {
	return Sample{Version: Version, ModuleID: c.ID, ComponentID: c.ComponentID, At: at, OK: true, Publish: true, Metrics: []Metric{{ID: "speed", Label: "Speed", Unit: "ratio", Value: speed}, {ID: "ready", Unit: "boolean", Value: 1}}}
}
func TestDurableDwellAndHysteresis(t *testing.T) {
	o, c := testOwner(t)
	for _, at := range []int64{100, 160, 190} {
		if e := o.ingest(sample(c, at, .7)); e != nil {
			t.Fatal(e)
		}
	}
	var state string
	o.db.QueryRow("SELECT state FROM telemetry_modules").Scan(&state)
	if state == "{}" {
		t.Fatal("policy not persisted")
	}
	for _, at := range []int64{220, 280, 340} {
		if e := o.ingest(sample(c, at, 1)); e != nil {
			t.Fatal(e)
		}
	}
	tx, _ := o.db.Begin()
	_, s, e := loadModule(tx, c.ID)
	tx.Rollback()
	if e != nil || s.Degraded {
		t.Fatalf("recovery state %#v, %v", s, e)
	}
}
func TestFailureAndAvailabilityGateResetDwellAndDiscardPending(t *testing.T) {
	for _, failed := range []bool{true, false} {
		o, c := testOwner(t)
		if e := o.ingest(sample(c, 100, .7)); e != nil {
			t.Fatal(e)
		}
		s := sample(c, 130, .7)
		s.Publish = false
		if failed {
			s.OK = false
			s.Metrics = nil
		}
		if e := o.ingest(s); e != nil {
			t.Fatal(e)
		}
		pending, e := o.pending(Query{Version: Version, ModuleID: c.ID, Now: 140})
		if e != nil || len(pending) != 0 {
			t.Fatal("stale delivery retained", e)
		}
		if e = o.ingest(sample(c, 160, .7)); e != nil {
			t.Fatal(e)
		}
		tx, _ := o.db.Begin()
		_, state, _ := loadModule(tx, c.ID)
		tx.Rollback()
		if state.BadSince != 160 || state.Degraded {
			t.Fatal(state)
		}
	}
}
func TestFailedPollPreservesHistoryAndExpiresSnapshot(t *testing.T) {
	o, c := testOwner(t)
	o.ingest(sample(c, 100, 1))
	s := sample(c, 150, 1)
	s.OK = false
	s.Metrics = nil
	o.ingest(s)
	view, e := o.snapshot(Query{Version: Version, ModuleID: c.ID, Now: 220})
	if e != nil || view.LastPollOK || !view.Stale || view.ObservedAt != 100 {
		t.Fatal(view, e)
	}
	history, e := o.history(Query{Version: Version, ModuleID: c.ID, Now: 220})
	if e != nil || len(history) != 1 {
		t.Fatal(history, e)
	}
}
func TestReplayAndLatePollCannotRewriteHealth(t *testing.T) {
	o, c := testOwner(t)
	o.ingest(sample(c, 100, 1))
	o.ingest(sample(c, 100, .2))
	o.ingest(sample(c, 90, .2))
	view, e := o.snapshot(Query{Version: Version, ModuleID: c.ID, Now: 110})
	if e != nil || view.Metrics[0].Value != 1 {
		t.Fatal(view, e)
	}
}
func TestInvalidMetricsAndIdentityAreAtomic(t *testing.T) {
	o, c := testOwner(t)
	for _, value := range []float64{math.NaN(), math.Inf(1)} {
		s := sample(c, 100, value)
		if o.ingest(s) == nil {
			t.Fatal("accepted nonfinite metric")
		}
	}
	s := sample(c, 100, 1)
	s.ComponentID = "other"
	if o.ingest(s) == nil {
		t.Fatal("accepted wrong component")
	}
	view, e := o.snapshot(Query{Version: Version, ModuleID: c.ID, Now: 110})
	if e != nil || view.LastAttempt != 0 {
		t.Fatal(view, e)
	}
}
func TestPendingReceiptsSurviveRestartAndAreAcknowledged(t *testing.T) {
	o, c := testOwner(t)
	o.ingest(sample(c, 100, 1))
	q := Query{Version: Version, ModuleID: c.ID, Now: 110}
	a, e := o.pending(q)
	if e != nil || len(a) != 1 {
		t.Fatal(a, e)
	}
	b, _ := o.pending(q)
	if a[0].ID != b[0].ID || a[0].Command != b[0].Command {
		t.Fatal("unstable delivery identity")
	}
	if e = o.ack(Ack{Version: Version, ModuleID: c.ID, ID: a[0].ID}); e != nil {
		t.Fatal(e)
	}
	b, _ = o.pending(q)
	if len(b) != 0 {
		t.Fatal("ack failed")
	}
}
func TestHigherThresholdAndCollectionGap(t *testing.T) {
	p := Policy{MetricID: "latency", Direction: "above", Above: 300, RecoverBelow: 150, BadFor: 120, GoodFor: 120}
	s := State{}
	for _, at := range []int64{100, 160, 220} {
		var e error
		s, _, e = advance(s, []Metric{{ID: "latency", Value: 400}}, at, p, 120)
		if e != nil {
			t.Fatal(e)
		}
	}
	if !s.Degraded {
		t.Fatal(s)
	}
	s, _, _ = advance(State{BadSince: 100, LastSample: 100}, []Metric{{ID: "latency", Value: 400}}, 400, p, 120)
	if s.BadSince != 400 || s.Degraded {
		t.Fatal(s)
	}
}
func TestFencedImportPreservesHistoryAndPolicy(t *testing.T) {
	o, c := testOwner(t)
	in := Import{Version: Version, ModuleID: c.ID, Samples: []HistorySample{{At: 100, Metrics: sample(c, 100, 1).Metrics}}}
	if e := o.importHistory(in); e != nil {
		t.Fatal(e)
	}
	in.Samples = nil
	in.Finish = true
	in.Attempted = 120
	in.OK = true
	in.State = State{Degraded: true, LastSample: 120}
	if e := o.importHistory(in); e != nil {
		t.Fatal(e)
	}
	if o.importHistory(in) == nil {
		t.Fatal("import reopened after cutover")
	}
	view, e := o.snapshot(Query{Version: Version, ModuleID: c.ID, Now: 130})
	if e != nil || view.ObservedAt != 100 || view.LastAttempt != 120 {
		t.Fatal(view, e)
	}
}
func TestStateSurvivesDatabaseReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	o, e := openOwner(path)
	if e != nil {
		t.Fatal(e)
	}
	c := Config{Version: Version, ID: "game", ComponentID: "game"}
	o.configure(c)
	o.ingest(sample(c, 100, 1))
	o.db.Close()
	o, e = openOwner(path)
	if e != nil {
		t.Fatal(e)
	}
	defer o.db.Close()
	view, e := o.snapshot(Query{Version: Version, ModuleID: c.ID, Now: 110})
	if e != nil || view.ObservedAt != 100 {
		t.Fatal(view, e)
	}
}

func TestEvaluationOnlyStoresPolicyWithoutCompetingMonitorObservation(t *testing.T) {
	o, e := openOwner(filepath.Join(t.TempDir(), "eval.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer o.db.Close()
	c := Config{Version: Version, ID: "query", ComponentID: "game", EvaluationOnly: true, Policy: Policy{Enabled: true, MetricID: "speed", SourceID: "source", Below: .8, RecoverAbove: .95}}
	if e = o.configure(c); e != nil {
		t.Fatal(e)
	}
	if e = o.ingest(sample(c, 100, .7)); e != nil {
		t.Fatal(e)
	}
	view, e := o.snapshot(Query{Version: Version, ModuleID: c.ID, Now: 110})
	if e != nil || view.PolicySignal != "degraded" {
		t.Fatal(view, e)
	}
	pending, e := o.pending(Query{Version: Version, ModuleID: c.ID, Now: 110})
	if e != nil || len(pending) != 0 {
		t.Fatal(pending, e)
	}
}

func TestHistoryIsByteBoundedAndKeepsNewestSamples(t *testing.T) {
	o, c := testOwner(t)
	metrics := []Metric{}
	for i := 0; i < 32; i++ {
		metrics = append(metrics, Metric{ID: "metric" + jsonInt(int64(i)), Label: strings.Repeat("<", 128), Unit: "count", Value: 1})
	}
	raw, _ := json.Marshal(metrics)
	tx, _ := o.db.Begin()
	for at := int64(100); at < 200; at++ {
		if _, e := tx.Exec("INSERT INTO telemetry_samples VALUES(?,?,?)", c.ID, at, string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	tx.Commit()
	history, e := o.history(Query{Version: Version, ModuleID: c.ID, Now: 200})
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(history)
	if len(encoded) > 512<<10 || len(history) == 0 || len(history) >= 100 || history[len(history)-1].At != 199 {
		t.Fatalf("history count=%d bytes=%d error=%v", len(history), len(encoded), e)
	}
}
