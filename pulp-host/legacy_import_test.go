package main

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	_ "modernc.org/sqlite"
)

type testLegacySource struct {
	rows []legacyImportRow
}

func (s *testLegacySource) ReadAfter(_ context.Context, cursor string, limit int) (legacyImportBatch, error) {
	start := 0
	if cursor != "" {
		start = len(s.rows)
		for index, row := range s.rows {
			if row.Cursor == cursor {
				start = index + 1
				break
			}
		}
	}
	end := start + limit
	if end > len(s.rows) {
		end = len(s.rows)
	}
	return legacyImportBatch{Rows: append([]legacyImportRow(nil), s.rows[start:end]...), Done: end == len(s.rows)}, nil
}

type testLegacyDestination struct {
	failAt int
	calls  []legacyImportEnvelope
}

func (d *testLegacyDestination) ApplyLegacyImport(_ context.Context, envelope legacyImportEnvelope) (legacyImportReceipt, error) {
	d.calls = append(d.calls, envelope)
	if d.failAt > 0 && len(d.calls) == d.failAt {
		return legacyImportReceipt{}, errors.New("temporary destination failure")
	}
	return legacyImportReceipt{ImportID: envelope.ImportID, Applied: true}, nil
}

type testLegacyVerifier struct {
	want  legacyImportSummary
	fail  bool
	calls int
}

func (v *testLegacyVerifier) VerifyLegacyImport(_ context.Context, summary legacyImportSummary) error {
	v.calls++
	if v.fail {
		return errors.New("destination count mismatch")
	}
	if !reflect.DeepEqual(summary, v.want) {
		return errors.New("unexpected invariant summary")
	}
	return nil
}

func TestLegacyImportResumesAfterLastAcknowledgedRowAndVerifies(t *testing.T) {
	rows := []legacyImportRow{
		{Cursor: "cursor-1", SortKey: "001", Entity: "component", LegacyID: "api", Identity: []byte("api/v1"), Payload: "private-one"},
		{Cursor: "cursor-2", SortKey: "002", Entity: "component", LegacyID: "database", Identity: []byte("database/v1"), Payload: "private-two"},
		{Cursor: "cursor-3", SortKey: "003", Entity: "source", LegacyID: "probe", Identity: []byte("probe/v1"), Payload: "private-three"},
	}
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "migration.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	checkpoints, err := newSQLiteLegacyImportCheckpointStore(db)
	if err != nil {
		t.Fatal(err)
	}
	firstDestination := &testLegacyDestination{failAt: 2}
	firstVerifier := &testLegacyVerifier{}
	service, err := newLegacyImportService(&testLegacySource{rows: rows}, firstDestination, checkpoints, firstVerifier, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background(), "status-v1"); err == nil {
		t.Fatal("expected temporary destination failure")
	}
	checkpoint, err := checkpoints.Load(context.Background(), "status-v1")
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Cursor != "cursor-1" || checkpoint.Applied != 1 || checkpoint.Completed {
		t.Fatalf("checkpoint after interruption = %#v", checkpoint)
	}

	wantSummary := legacyImportSummary{
		Migration: "status-v1",
		Applied:   3,
		Digest: advanceLegacyImportDigest(
			advanceLegacyImportDigest(
				advanceLegacyImportDigest("", stableLegacyImportID(rows[0])),
				stableLegacyImportID(rows[1]),
			),
			stableLegacyImportID(rows[2]),
		),
	}
	secondDestination := &testLegacyDestination{}
	secondVerifier := &testLegacyVerifier{want: wantSummary}
	resumed, err := newLegacyImportService(&testLegacySource{rows: rows}, secondDestination, checkpoints, secondVerifier, 1)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := resumed.Run(context.Background(), "status-v1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(summary, wantSummary) {
		t.Fatalf("summary = %#v, want %#v", summary, wantSummary)
	}
	if len(secondDestination.calls) != 2 || secondDestination.calls[0].LegacyID != "database" {
		t.Fatalf("resume calls = %#v", secondDestination.calls)
	}
	checkpoint, err = checkpoints.Load(context.Background(), "status-v1")
	if err != nil {
		t.Fatal(err)
	}
	if !checkpoint.Completed || secondVerifier.calls != 1 {
		t.Fatalf("completion checkpoint = %#v, verifier calls = %d", checkpoint, secondVerifier.calls)
	}
	if _, err := resumed.Run(context.Background(), "status-v1"); err != nil {
		t.Fatal(err)
	}
	if len(secondDestination.calls) != 2 || secondVerifier.calls != 1 {
		t.Fatal("completed migration performed work again")
	}
}

func TestLegacyImportRejectsUnstableOrderingBeforeDispatch(t *testing.T) {
	checkpoints := newMemoryLegacyCheckpointStore()
	destination := &testLegacyDestination{}
	verifier := &testLegacyVerifier{}
	source := &testLegacySource{rows: []legacyImportRow{
		{Cursor: "2", SortKey: "002", Entity: "component", LegacyID: "two", Identity: []byte("two")},
		{Cursor: "1", SortKey: "001", Entity: "component", LegacyID: "one", Identity: []byte("one")},
	}}
	service, err := newLegacyImportService(source, destination, checkpoints, verifier, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background(), "unordered"); err == nil {
		t.Fatal("expected ordering failure")
	}
	if len(destination.calls) != 1 {
		t.Fatalf("destination calls = %d, want first row only", len(destination.calls))
	}
}

func TestLegacyImportDoesNotCompleteUntilInvariantsPass(t *testing.T) {
	checkpoints := newMemoryLegacyCheckpointStore()
	row := legacyImportRow{Cursor: "1", SortKey: "001", Entity: "subscriber", LegacyID: "sub-1", Identity: []byte("sub-1/v1")}
	destination := &testLegacyDestination{}
	failing := &testLegacyVerifier{fail: true}
	service, err := newLegacyImportService(&testLegacySource{rows: []legacyImportRow{row}}, destination, checkpoints, failing, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background(), "subscriber-v1"); err == nil {
		t.Fatal("expected invariant failure")
	}
	checkpoint, _ := checkpoints.Load(context.Background(), "subscriber-v1")
	if checkpoint.Completed || checkpoint.Applied != 1 {
		t.Fatalf("checkpoint = %#v", checkpoint)
	}

	want := checkpoint.summary()
	passing := &testLegacyVerifier{want: want}
	resumed, err := newLegacyImportService(&testLegacySource{rows: []legacyImportRow{row}}, destination, checkpoints, passing, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resumed.Run(context.Background(), "subscriber-v1"); err != nil {
		t.Fatal(err)
	}
	if len(destination.calls) != 1 {
		t.Fatal("invariant retry re-dispatched an acknowledged row")
	}
}

func TestStableLegacyImportIDExcludesPrivatePayload(t *testing.T) {
	row := legacyImportRow{Entity: "auth/api-token", LegacyID: "token-1", Identity: []byte("token-1/v2"), Payload: "first-secret"}
	first := stableLegacyImportID(row)
	row.Payload = "different-secret"
	if second := stableLegacyImportID(row); second != first {
		t.Fatalf("private payload changed stable import ID: %q != %q", first, second)
	}
	row.Identity = []byte("token-1/v3")
	if stableLegacyImportID(row) == first {
		t.Fatal("non-secret identity version did not change stable import ID")
	}
}

func TestLegacyImportGrantsDirectTargetsOnlyToExactStatusProber(t *testing.T) {
	for _, test := range []struct {
		name    string
		kind    string
		revoked bool
		want    bool
	}{
		{name: "Status Prober", kind: "probe", want: true},
		{name: "status prober", kind: "probe"},
		{name: "Status Prober ", kind: "probe"},
		{name: "Status Prober", kind: "push"},
		{name: "Status Prober", kind: "probe", revoked: true},
		{name: "Vendor", kind: "probe"},
	} {
		if got := legacyMonitorSourceDirectTargets(test.name, test.kind, test.revoked); got != test.want {
			t.Fatalf("direct target capability for %#v = %v, want %v", test, got, test.want)
		}
	}
}

type memoryLegacyCheckpointStore struct {
	values map[string]legacyImportCheckpoint
}

func newMemoryLegacyCheckpointStore() *memoryLegacyCheckpointStore {
	return &memoryLegacyCheckpointStore{values: map[string]legacyImportCheckpoint{}}
}

func (s *memoryLegacyCheckpointStore) Load(_ context.Context, migration string) (legacyImportCheckpoint, error) {
	value := s.values[migration]
	if value.Migration == "" {
		value.Migration = migration
	}
	return value, nil
}

func (s *memoryLegacyCheckpointStore) Save(_ context.Context, checkpoint legacyImportCheckpoint) error {
	s.values[checkpoint.Migration] = checkpoint
	return nil
}
