package main

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type fakeLegacyPhaseReader struct {
	records map[string][]legacyPhaseRecord
}

func (r *fakeLegacyPhaseReader) ReadLegacyPhase(
	_ context.Context,
	phase string,
	after string,
	limit int,
) ([]legacyPhaseRecord, error) {
	var result []legacyPhaseRecord
	for _, record := range r.records[phase] {
		if record.SortKey > after {
			result = append(result, record)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

func TestLegacyBananapulseRowSourceUsesFixedPhaseOrderAndResumableCursor(t *testing.T) {
	reader := &fakeLegacyPhaseReader{records: map[string][]legacyPhaseRecord{
		"component": {{SortKey: "0000000000:root", ID: "root", Payload: json.RawMessage(`{"id":"root"}`)}},
		"source":    {{SortKey: "probe", ID: "probe", Payload: json.RawMessage(`{"id":"probe","name":"Status Prober","kind":"probe"}`)}},
	}}
	source, err := newLegacyBananapulseRowSource(reader)
	if err != nil {
		t.Fatal(err)
	}
	first, err := source.ReadAfter(context.Background(), "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Rows) != 1 || first.Rows[0].Entity != "component" || first.Done {
		t.Fatalf("first batch = %#v", first)
	}
	second, err := source.ReadAfter(context.Background(), first.Rows[0].Cursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Rows) != 1 || second.Rows[0].Entity != "source" || second.Done {
		t.Fatalf("second batch = %#v", second)
	}
	last, err := source.ReadAfter(context.Background(), second.Rows[0].Cursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Rows) != 0 || !last.Done {
		t.Fatalf("terminal batch = %#v", last)
	}
}

func TestPostgresLegacyPhaseQueriesAreReadOnlyAndComplete(t *testing.T) {
	for _, phase := range legacyBananapulsePhases {
		query, ok := legacyBananapulsePhaseSQL[phase]
		if !ok || strings.TrimSpace(query) == "" {
			t.Fatalf("missing SQL for phase %q", phase)
		}
		upper := strings.ToUpper(query)
		for _, forbidden := range []string{"INSERT ", "UPDATE ", "DELETE ", "ALTER ", "DROP "} {
			if strings.Contains(upper, forbidden) {
				t.Fatalf("phase %q contains write SQL %q", phase, forbidden)
			}
		}
	}
}

type recordingLegacyPulpClient struct {
	appEvents      []string
	appRequests    []any
	providerNames  []string
	providerInputs []any
}

func (c *recordingLegacyPulpClient) callRaw(_ context.Context, event string, request any, _ any) error {
	c.appEvents = append(c.appEvents, event)
	c.appRequests = append(c.appRequests, request)
	return nil
}

func (c *recordingLegacyPulpClient) callProviderRaw(
	_ context.Context,
	_ string,
	provider string,
	request any,
	_ any,
) error {
	c.providerNames = append(c.providerNames, provider)
	c.providerInputs = append(c.providerInputs, request)
	return nil
}

func TestLegacyDestinationSplitsSourceCredentialFromMonitorMetadata(t *testing.T) {
	for _, test := range []struct {
		name       string
		kind       string
		revokedAt  any
		wantDirect bool
	}{
		{name: "Status Prober", kind: "probe", wantDirect: true},
		{name: "status prober", kind: "probe"},
		{name: "Status Prober", kind: "push"},
		{name: "Status Prober", kind: "probe", revokedAt: "2026-07-26T02:00:00Z"},
	} {
		client := &recordingLegacyPulpClient{}
		destination, err := newLegacyPulpDestination(client)
		if err != nil {
			t.Fatal(err)
		}
		row := map[string]any{
			"id": "source-1", "name": test.name, "token_hash": strings.Repeat("a", 64),
			"weight": 1, "kind": test.kind, "trusted": true,
			"created_at": "2026-07-26T01:00:00Z", "revoked_at": test.revokedAt,
		}
		payload, _ := json.Marshal(row)
		_, err = destination.ApplyLegacyImport(context.Background(), legacyImportEnvelope{
			ImportID: "import-source-1", Entity: "source", LegacyID: "source-1", Payload: json.RawMessage(payload),
		})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(client.providerNames, []string{providerAuthSourceAdminImport}) ||
			!reflect.DeepEqual(client.appEvents, []string{eventMonitorMigrationImport}) {
			t.Fatalf("owner routing providers=%#v events=%#v", client.providerNames, client.appEvents)
		}
		authRequest := client.providerInputs[0].(bridgeAuthSourceCredentialImportRequest)
		if authRequest.Token != "" || authRequest.TokenDigest != strings.Repeat("a", 64) {
			t.Fatalf("credential import = %#v", authRequest)
		}
		command := client.appRequests[0].(bridgeMonitorCommand)
		if command.Source == nil || command.Source.DirectTargets != test.wantDirect {
			t.Fatalf("monitor source = %#v, want direct=%v", command.Source, test.wantDirect)
		}
	}
}

func TestLegacyDestinationMapsAllStateOwnerPhases(t *testing.T) {
	now := "2026-07-26T01:00:00Z"
	rows := map[string]string{
		"component":         `{"id":"root","name":"Root","kind":"organization","status":"ok","uptime_90d":["ok",{"date":"2026-07-26","status":"out"}],"launched":true,"created_at":"` + now + `"}`,
		"mapping":           `{"id":"m1","source_id":"s1","raw_label":"api","component_id":"c1"}`,
		"observation":       `{"id":"o1","source_id":"s1","component_id":"c1","signal":"ok","observed_at":"` + now + `"}`,
		"incident":          `{"id":"i1","title":"Incident","summary":"Summary","status":"investigating","severity":"minor","affects":["c1"],"started_at":"` + now + `","created_at":"` + now + `"}`,
		"incident_update":   `{"id":"u1","incident_id":"i1","at":"` + now + `","label":"update","body":"Body","author":"admin"}`,
		"maintenance":       `{"id":"w1","title":"Window","summary":"Summary","kind":"scheduled","scheduled_start":"` + now + `","scheduled_end":"2026-07-26T02:00:00Z","affects":["c1"],"created_at":"` + now + `"}`,
		"component_archive": `{"id":"c1","archived_at":"` + now + `"}`,
		"source_revoke":     `{"id":"s1","revoked_at":"` + now + `"}`,
		"subscriber":        `{"id":"sub1","email":"owner@example.test","confirmed_at":"` + now + `","created_at":"` + now + `"}`,
		"api_token":         `{"id":"api1","name":"API","token_hash":"` + strings.Repeat("b", 64) + `","scope":"full","created_at":"` + now + `"}`,
	}
	client := &recordingLegacyPulpClient{}
	destination, _ := newLegacyPulpDestination(client)
	for entity, raw := range rows {
		if _, err := destination.ApplyLegacyImport(context.Background(), legacyImportEnvelope{
			ImportID: "import-" + entity, Entity: entity, LegacyID: entity, Payload: json.RawMessage(raw),
		}); err != nil {
			t.Fatalf("%s mapping failed: %v", entity, err)
		}
	}
	if len(client.appEvents) != 9 || len(client.providerNames) != 1 {
		t.Fatalf("mapped app=%d provider=%d", len(client.appEvents), len(client.providerNames))
	}
}

func TestBridgeUptimeDayPreservesLegacyStringAndStructuredDay(t *testing.T) {
	var values []bridgeUptimeDay
	if err := json.Unmarshal([]byte(`["ok",{"date":"2026-07-26","status":"out"}]`), &values); err != nil {
		t.Fatal(err)
	}
	if values[0].LegacyStatus != "ok" || values[1].Date != "2026-07-26" {
		t.Fatalf("uptime values = %#v", values)
	}
}

func TestLegacyIdentityDoesNotContainCredentialDigestOrSubscriberEmail(t *testing.T) {
	sourcePayload := json.RawMessage(`{"id":"s1","name":"Probe","token_hash":"` + strings.Repeat("c", 64) + `","kind":"probe","created_at":"2026-07-26T01:00:00Z"}`)
	sourceIdentity, err := legacyBananapulseIdentity("source", "s1", sourcePayload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sourceIdentity), strings.Repeat("c", 64)) {
		t.Fatal("source import identity contains credential digest")
	}
	subscriberPayload := json.RawMessage(`{"id":"sub1","email":"private@example.test","created_at":"2026-07-26T01:00:00Z"}`)
	subscriberIdentity, err := legacyBananapulseIdentity("subscriber", "sub1", subscriberPayload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(subscriberIdentity), "private@example.test") {
		t.Fatal("subscriber import identity contains email")
	}
}
