package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

type memoryEventStore struct {
	migrated  bool
	events    []Command
	appendErr error
}

func (s *memoryEventStore) Migrate(context.Context) error { s.migrated = true; return nil }
func (s *memoryEventStore) Load(context.Context) ([]Command, error) {
	return append([]Command(nil), s.events...), nil
}
func (s *memoryEventStore) Append(_ context.Context, command Command, _ CommandResult) error {
	if s.appendErr != nil {
		return s.appendErr
	}
	s.events = append(s.events, command)
	return nil
}

func command(id string, kind CommandKind, at int64) Command {
	return Command{Version: ContractVersion, ID: id, Kind: kind, AtUnix: at}
}
func seconds(value int64) *int64 { return &value }
func apply(t *testing.T, cell *owner, command Command) CommandResult {
	t.Helper()
	result, err := cell.apply(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestQuorumTrustedWeightedAndTTL(t *testing.T) {
	store := &memoryEventStore{}
	cell, err := openOwner(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	component := command("component", UpsertComponent, 100)
	component.Component = &Component{ID: "api", Name: "API", Critical: true}
	apply(t, cell, component)
	for _, source := range []Source{{ID: "external", Name: "external", Weight: 1, Kind: SourceProbe, DefaultTTL: seconds(30)}, {ID: "self", Name: "self", Weight: 1, Kind: SourcePush, Trusted: true, DefaultTTL: seconds(30)}, {ID: "weighted", Name: "weighted", Weight: 2, Kind: SourceProbe, DefaultTTL: seconds(30)}} {
		c := command("source/"+source.ID, UpsertSource, 100)
		c.Source = &source
		apply(t, cell, c)
	}
	observation := command("external/down", AppendObservation, 101)
	observation.Observation = &Observation{ID: "external/down", SourceID: "external", ComponentID: "api", Signal: SignalDown, ObservedAtUnix: 101, ExpiresAtUnix: 105}
	apply(t, cell, observation)
	if got := cell.projection(102).Components[0].Evaluation; got.State != "watch" || got.Status != StatusOperational {
		t.Fatalf("untrusted singleton = %#v", got)
	}
	trusted := command("self/down", AppendObservation, 102)
	trusted.Observation = &Observation{ID: "self/down", SourceID: "self", ComponentID: "api", Signal: SignalDown, ObservedAtUnix: 106}
	apply(t, cell, trusted)
	if got := cell.projection(110).Components[0].Evaluation; got.State != "declared" || got.Status != StatusOutage {
		t.Fatalf("trusted singleton must preserve signal severity: %#v", got)
	}
	weighted := command("weighted/down", AppendObservation, 103)
	weighted.Observation = &Observation{ID: "weighted/down", SourceID: "weighted", ComponentID: "api", Signal: SignalDown, ObservedAtUnix: 111}
	apply(t, cell, weighted)
	if got := cell.projection(112).Components[0].Evaluation; got.Status != StatusOutage || got.NonOKWeight != 3 {
		t.Fatalf("weighted quorum did not declare outage: %#v", got)
	}
	if got := cell.projection(142).Components[0].Evaluation; !got.ReducedCoverage || got.HasLiveReads {
		t.Fatalf("TTL expiry was not reflected: %#v", got)
	}
}

func TestComponentTreeBubblesWorstChild(t *testing.T) {
	store := &memoryEventStore{}
	cell, err := openOwner(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range []Component{{ID: "org", Name: "Organization"}, {ID: "product", ParentID: "org", Name: "Product"}, {ID: "api", ParentID: "product", Name: "API"}} {
		c := command("component/"+component.ID, UpsertComponent, 100)
		c.Component = &component
		apply(t, cell, c)
	}
	source := command("source", UpsertSource, 100)
	source.Source = &Source{ID: "s", Name: "manual", Weight: 1, Kind: SourceManual}
	apply(t, cell, source)
	obs := command("manual/down", AppendObservation, 101)
	obs.Observation = &Observation{ID: "manual/down", SourceID: "s", ComponentID: "api", Signal: SignalDown, ObservedAtUnix: 101}
	apply(t, cell, obs)
	projection := cell.projection(102)
	statuses := map[string]Status{}
	for _, component := range projection.Components {
		statuses[component.Component.ID] = component.Evaluation.Status
	}
	want := map[string]Status{"org": StatusOutage, "product": StatusOutage, "api": StatusOutage}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("tree did not bubble: got %#v want %#v", statuses, want)
	}
	for _, component := range projection.Components {
		if component.Component.ID != "api" && component.OwnEvaluation.Status != StatusOperational {
			t.Fatalf("%s own evaluation was overwritten by tree rollup: %#v", component.Component.ID, component.OwnEvaluation)
		}
	}
}

func TestProjectionPreservesApplicationMetadataAndSortOrder(t *testing.T) {
	store := &memoryEventStore{}
	cell, err := openOwner(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range []Component{
		{ID: "later", Name: "Later", Tag: "api", SortOrder: 20},
		{ID: "first", Name: "First", Tag: "web", SortOrder: 10},
	} {
		c := command("component/"+component.ID, UpsertComponent, 100)
		c.Component = &component
		apply(t, cell, c)
	}
	projection := cell.projection(101)
	if got := []string{projection.Components[0].Component.ID, projection.Components[1].Component.ID}; !reflect.DeepEqual(got, []string{"first", "later"}) {
		t.Fatalf("projection order = %#v", got)
	}
	if projection.Components[0].Component.Tag != "web" {
		t.Fatalf("component tag was not preserved: %#v", projection.Components[0].Component)
	}
}

func TestReplayRestartAndCommandIdempotency(t *testing.T) {
	store := &memoryEventStore{}
	first, err := openOwner(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	c := command("component", UpsertComponent, 100)
	c.Component = &Component{ID: "api", Name: "API"}
	firstResult := apply(t, first, c)
	duplicate := apply(t, first, c)
	if !duplicate.Deduped || duplicate.Revision != firstResult.Revision || len(store.events) != 1 {
		t.Fatalf("idempotency failed: first=%#v duplicate=%#v events=%d", firstResult, duplicate, len(store.events))
	}
	restarted, err := openOwner(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if got := restarted.projection(101); got.Revision != 1 || len(got.Components) != 1 {
		t.Fatalf("restart replay failed: %#v", got)
	}
	store.appendErr = errors.New("disk unavailable")
	next := command("source", UpsertSource, 102)
	next.Source = &Source{ID: "s", Name: "source", Weight: 1}
	if _, err := restarted.apply(context.Background(), next); err == nil {
		t.Fatal("expected persistence failure")
	}
	if got := restarted.projection(102); got.Revision != 1 || len(got.Sources) != 0 {
		t.Fatalf("undurable change leaked: %#v", got)
	}
}

func TestIncidentAndMaintenanceLifecycle(t *testing.T) {
	store := &memoryEventStore{}
	cell, err := openOwner(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	c := command("component", UpsertComponent, 100)
	c.Component = &Component{ID: "api", Name: "API"}
	apply(t, cell, c)
	open := command("incident/open", OpenIncident, 101)
	open.Incident = &Incident{ID: "human/1", Title: "API issue", Summary: "Investigating", Affects: []string{"api"}}
	apply(t, cell, open)
	update := command("incident/update", UpdateIncident, 102)
	update.Update = &IncidentUpdate{ID: "human/1/update", IncidentID: "human/1", AtUnix: 102, Label: "identified", Body: "Cause known", Author: "operator"}
	apply(t, cell, update)
	resolve := command("incident/resolve", ResolveIncident, 103)
	resolve.Incident = &Incident{ID: "human/1"}
	apply(t, cell, resolve)
	maintenance := command("maintenance", ScheduleMaintenance, 104)
	maintenance.Maintenance = &Maintenance{ID: "m1", Title: "Routine", Summary: "", ScheduledStartUnix: 200, ScheduledEndUnix: 300, Affects: []string{"api"}}
	apply(t, cell, maintenance)
	projection := cell.projection(104)
	if projection.Incidents[0].ResolvedAtUnix != 103 || len(projection.IncidentUpdates) != 3 || len(projection.Maintenance) != 1 {
		t.Fatalf("lifecycle projection: %#v", projection)
	}
}

func TestSQLiteStoreSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "monitor.db")
	store, err := newSQLiteEventStore(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := openOwner(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	c := command("component", UpsertComponent, 100)
	c.Component = &Component{ID: "api", Name: "API"}
	apply(t, first, c)
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopenedStore, err := newSQLiteEventStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopenedStore.db.Close()
	restarted, err := openOwner(context.Background(), reopenedStore)
	if err != nil {
		t.Fatal(err)
	}
	if got := restarted.projection(101); got.Revision != 1 || len(got.Components) != 1 || got.Components[0].Component.ID != "api" {
		t.Fatalf("SQLite replay failed: %#v", got)
	}
}

func TestMessagePackContractIsTaggedAndRoundTrips(t *testing.T) {
	in := Command{Version: ContractVersion, ID: "component", Kind: UpsertComponent, AtUnix: 100, Component: &Component{ID: "api", Name: "API"}}
	raw, err := msgpack.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := msgpack.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "id", "kind", "at_unix", "component"} {
		if _, ok := wire[key]; !ok {
			t.Fatalf("MessagePack command omitted %q: %#v", key, wire)
		}
	}
	var out Command
	if err := msgpack.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("MessagePack round trip changed contract: in=%#v out=%#v", in, out)
	}
}

func TestMessagePackPreservesMixedUptimeHistoryAndNullableTTL(t *testing.T) {
	in := struct {
		Component Component `msgpack:"component"`
		Source    Source    `msgpack:"source"`
	}{
		Component: Component{ID: "api", Name: "API", Uptime90D: []UptimeDay{{LegacyStatus: "ok"}, {Date: "2026-07-26", Status: "out"}}},
		Source:    Source{ID: "manual", Name: "Manual", Weight: 1, Kind: SourceManual, DefaultTTL: nil},
	}
	raw, err := msgpack.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Component Component `msgpack:"component"`
		Source    Source    `msgpack:"source"`
	}
	if err := msgpack.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("lossless metadata round trip failed: in=%#v out=%#v", in, out)
	}
	var wire map[string]any
	if err := msgpack.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	source, ok := wire["source"].(map[string]any)
	if !ok {
		t.Fatalf("source wire shape = %#v", wire["source"])
	}
	if value, exists := source["default_ttl_seconds"]; !exists || value != nil {
		t.Fatalf("nullable TTL must be explicitly encoded as nil: %#v", source)
	}
}

func TestComponentMetadataSafeEditAndGuardedArchiveRestoreBatch(t *testing.T) {
	cell, err := openOwner(context.Background(), &memoryEventStore{})
	if err != nil {
		t.Fatal(err)
	}
	components := []Component{
		{ID: "root", Name: "Banana Labs", Kind: "organization", Brand: "banana", Domain: "status.example", Launched: true, LaunchedSet: true, Uptime90D: []UptimeDay{{LegacyStatus: "ok"}, {Date: "2026-07-26", Status: "deg"}}, SortOrder: 1},
		{ID: "api", ParentID: "root", Name: "API", Kind: "service", Tag: "runtime", Launched: true, SortOrder: 2},
		{ID: "retired", ParentID: "root", Name: "Retired", Kind: "service", Launched: false, LaunchedSet: true, SortOrder: 3},
	}
	for i := range components {
		c := command("component/"+components[i].ID, UpsertComponent, 100)
		c.Component = &components[i]
		apply(t, cell, c)
	}
	name, domain, launched := "API v2", "api.example", false
	edit := command("component/api/edit", EditComponent, 101)
	edit.ComponentPatch = &ComponentPatch{ID: "api", Name: &name, Domain: &domain, Launched: &launched}
	apply(t, cell, edit)
	got := cell.projection(101).Components[1].Component
	if got.Name != name || got.Domain != domain || got.Launched || got.Tag != "runtime" {
		t.Fatalf("safe component edit lost metadata: %#v", got)
	}
	root := cell.projection(101).Components[0].Component
	if !root.Launched || !reflect.DeepEqual(root.Uptime90D, []UptimeDay{{LegacyStatus: "ok"}, {Date: "2026-07-26", Status: "deg"}}) {
		t.Fatalf("component defaults/object uptime history were not lossless: %#v", root)
	}
	cycleParent := "api"
	cycle := command("component/root/cycle", EditComponent, 102)
	cycle.ComponentPatch = &ComponentPatch{ID: "root", ParentID: &cycleParent}
	if _, err := cell.apply(context.Background(), cycle); err == nil {
		t.Fatal("expected component cycle rejection")
	}

	retire := command("archive/retired", ArchiveComponent, 103)
	retire.ComponentID, retire.ArchiveBatchID = "retired", "retired/103"
	apply(t, cell, retire)
	open := command("incident/open", OpenIncident, 104)
	open.Incident = &Incident{ID: "manual/1", Title: "API outage", Summary: "Investigating", Severity: "major", Affects: []string{"api"}}
	apply(t, cell, open)
	blocked := command("archive/root/blocked", ArchiveComponent, 105)
	blocked.ComponentID, blocked.ArchiveBatchID = "root", "root/105"
	if _, err := cell.apply(context.Background(), blocked); err == nil {
		t.Fatal("expected subtree archive to reject a live incident")
	}
	remove := command("incident/delete", DeleteIncident, 106)
	remove.IncidentID = "manual/1"
	apply(t, cell, remove)
	archive := command("archive/root", ArchiveComponent, 107)
	archive.ComponentID, archive.ArchiveBatchID = "root", "root/107"
	archiveResult := apply(t, cell, archive)
	if !reflect.DeepEqual(archiveResult.ComponentIDs, []string{"root", "api"}) {
		t.Fatalf("archive batch changed wrong nodes: %#v", archiveResult.ComponentIDs)
	}
	restore := command("restore/root", RestoreComponent, 108)
	restore.ComponentID = "root"
	restoreResult := apply(t, cell, restore)
	if !reflect.DeepEqual(restoreResult.ComponentIDs, []string{"root", "api"}) {
		t.Fatalf("restore batch changed wrong nodes: %#v", restoreResult.ComponentIDs)
	}
	all, err := cell.query(Query{Version: ContractVersion, AtUnix: 109, IncludeArchived: true})
	if err != nil {
		t.Fatal(err)
	}
	archived := map[string]bool{}
	for _, component := range all.Components {
		archived[component.Component.ID] = component.Component.Archived
	}
	if !reflect.DeepEqual(archived, map[string]bool{"api": false, "retired": true, "root": false}) {
		t.Fatalf("batch-scoped restore revived an independently retired child: %#v", archived)
	}
}

func TestSourceLifecycleMappingAndAuthenticatedIngestCompatibility(t *testing.T) {
	cell, err := openOwner(context.Background(), &memoryEventStore{})
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range []Component{{ID: "api", Name: "API", Kind: "service"}, {ID: "web", Name: "Web", Kind: "service"}} {
		c := command("component/"+component.ID, UpsertComponent, 100)
		c.Component = &component
		apply(t, cell, c)
	}
	source := command("source/create", UpsertSource, 100)
	source.Source = &Source{ID: "probe", Name: "First party", Kind: SourceKind(" Vendor.Probe "), Weight: 1, Trusted: true, DefaultTTL: seconds(30)}
	apply(t, cell, source)
	weight, ttl := 2, int64(45)
	edit := command("source/edit", EditSource, 101)
	edit.SourcePatch = &SourcePatch{ID: "probe", Weight: &weight, DefaultTTL: &ttl, DefaultTTLSet: true}
	apply(t, cell, edit)
	if got := cell.projection(101).Sources[0].Kind; got != SourceKind("vendor.probe") {
		t.Fatalf("arbitrary safe source kind was not normalized: %q", got)
	}
	mapping := command("mapping/create", MapSourceTarget, 101)
	mapping.Mapping = &SourceTargetMapping{ID: "map/1", SourceID: "probe", RawLabel: "payments", ComponentID: "api"}
	apply(t, cell, mapping)
	ingest := command("ingest/1", IngestObservation, 102)
	ingest.Ingest = &IngestRequest{ObservationID: "obs/1", SourceID: "probe", RawLabel: "payments", Signal: SignalDown}
	result := apply(t, cell, ingest)
	if result.Evaluation == nil || result.Evaluation.ComponentID != "api" || result.Evaluation.State != "declared" || result.Evaluation.Level != "major" || result.Evaluation.Sources != 1 {
		t.Fatalf("ingest compatibility result = %#v", result.Evaluation)
	}
	if len(result.Transitions) != 1 || result.Transitions[0].Kind != "incident.opened" {
		t.Fatalf("ingest transition = %#v", result.Transitions)
	}
	if transition := result.Transitions[0]; transition.ID != "ingest/1/incident.opened/auto/api/102" || transition.Incident == nil || !reflect.DeepEqual(transition.AffectedComponentIDs, []string{"api"}) {
		t.Fatalf("ingest transition lacks stable notification data: %#v", transition)
	}

	remap := command("mapping/remap", MapSourceTarget, 103)
	remap.Mapping = &SourceTargetMapping{ID: "map/2", SourceID: "probe", RawLabel: "payments", ComponentID: "web"}
	remapResult := apply(t, cell, remap)
	if remapResult.MappingID != "map/1" {
		t.Fatalf("remap result = %#v", remapResult)
	}
	projection, err := cell.query(Query{Version: ContractVersion, SourceID: "probe", IncludeObservations: true, AtUnix: 103})
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Mappings) != 1 || projection.Mappings[0].ID != "map/1" || len(projection.Observations) != 1 {
		t.Fatalf("source projection is not exact after remap: %#v", projection)
	}
	clearTTL := command("source/clear-ttl", EditSource, 104)
	clearTTL.SourcePatch = &SourcePatch{ID: "probe", DefaultTTLSet: true}
	apply(t, cell, clearTTL)
	if got := cell.projection(104).Sources[0].DefaultTTL; got != nil {
		t.Fatalf("nullable default TTL was not cleared: %#v", got)
	}
	revoke := command("source/revoke", RevokeSource, 105)
	revoke.SourceID = "probe"
	apply(t, cell, revoke)
	rejected := command("ingest/revoked", IngestObservation, 106)
	rejected.Ingest = &IngestRequest{ObservationID: "obs/2", SourceID: "probe", RawLabel: "payments", Signal: SignalOK}
	if _, err := cell.apply(context.Background(), rejected); err == nil {
		t.Fatal("expected revoked source ingest rejection")
	}
	restore := command("source/restore", RestoreSource, 107)
	restore.SourceID = "probe"
	apply(t, cell, restore)
	unmap := command("mapping/unmap", UnmapSourceTarget, 108)
	unmap.MappingID = "map/1"
	apply(t, cell, unmap)
	if got := cell.projection(108); len(got.Mappings) != 0 || got.Sources[0].CreatedAtUnix != 100 || got.Sources[0].Revoked || got.Sources[0].DefaultTTL != nil {
		t.Fatalf("source lifecycle projection = %#v", got)
	}
}

func TestIncidentMaintenanceLifecyclesTransitionsAndImportSuppression(t *testing.T) {
	store := &memoryEventStore{}
	cell, err := openOwner(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	component := command("component", UpsertComponent, 100)
	component.Component = &Component{ID: "api", Name: "API", Kind: "service"}
	apply(t, cell, component)

	open := command("incident/open", OpenIncident, 101)
	open.Incident = &Incident{ID: "i1", Title: "Outage", Summary: "Investigating", Severity: "major", Affects: []string{"api"}}
	if got := apply(t, cell, open).Transitions; len(got) != 1 || got[0].Kind != "incident.opened" {
		t.Fatalf("open transitions = %#v", got)
	}
	status, title := "identified", "Database outage"
	edit := command("incident/edit", EditIncident, 102)
	edit.IncidentPatch = &IncidentPatch{ID: "i1", Status: &status, Title: &title, Note: "Root cause found.", AtUnix: 102, Author: "operator"}
	if got := apply(t, cell, edit).Transitions; len(got) != 1 || got[0].Kind != "incident.updated" || got[0].Status != "identified" {
		t.Fatalf("edit transitions = %#v", got)
	}
	timeline := command("incident/timeline", UpdateIncident, 102)
	timeline.Update = &IncidentUpdate{ID: "i1/timeline", IncidentID: "i1", AtUnix: 102, Label: "RESOLVED", Body: "Narrative only.", Author: "operator"}
	if got := apply(t, cell, timeline).Transitions; len(got) != 1 || got[0].Kind != "incident.updated" || got[0].IncidentUpdate == nil {
		t.Fatalf("timeline transitions = %#v", got)
	}
	if got := cell.projection(102).Incidents[0].Status; got != "identified" {
		t.Fatalf("timeline label silently changed incident status to %q", got)
	}
	resolve := command("incident/resolve", ResolveIncident, 103)
	resolve.Incident = &Incident{ID: "i1"}
	if got := apply(t, cell, resolve).Transitions; len(got) != 1 || got[0].Kind != "incident.resolved" {
		t.Fatalf("resolve transitions = %#v", got)
	}
	remove := command("incident/delete", DeleteIncident, 104)
	remove.IncidentID = "i1"
	if got := apply(t, cell, remove).Transitions; len(got) != 1 || got[0].Kind != "incident.deleted" {
		t.Fatalf("delete transitions = %#v", got)
	}

	maintenance := command("maintenance/create", ScheduleMaintenance, 105)
	maintenance.Maintenance = &Maintenance{ID: "m1", Title: "Upgrade", Summary: "Routine", ScheduledStartUnix: 200, ScheduledEndUnix: 300, Affects: []string{"api"}}
	if got := apply(t, cell, maintenance).Transitions; len(got) != 1 || got[0].Kind != "maintenance.created" {
		t.Fatalf("maintenance create transitions = %#v", got)
	}
	summary := "Extended upgrade"
	end := int64(350)
	maintenanceEdit := command("maintenance/edit", EditMaintenance, 106)
	maintenanceEdit.MaintenancePatch = &MaintenancePatch{ID: "m1", Summary: &summary, ScheduledEndUnix: &end}
	if got := apply(t, cell, maintenanceEdit).Transitions; len(got) != 1 || got[0].Kind != "maintenance.updated" {
		t.Fatalf("maintenance edit transitions = %#v", got)
	}
	cancel := command("maintenance/cancel", CancelMaintenance, 107)
	cancel.MaintenanceID = "m1"
	if got := apply(t, cell, cancel).Transitions; len(got) != 1 || got[0].Kind != "maintenance.cancelled" {
		t.Fatalf("maintenance cancel transitions = %#v", got)
	}
	deleteMaintenance := command("maintenance/delete", DeleteMaintenance, 108)
	deleteMaintenance.MaintenanceID = "m1"
	if got := apply(t, cell, deleteMaintenance).Transitions; len(got) != 1 || got[0].Kind != "maintenance.deleted" {
		t.Fatalf("maintenance delete transitions = %#v", got)
	}

	imported := command("incident/import", OpenIncident, 109)
	imported.ImportMode = true
	imported.Incident = &Incident{ID: "imported", Title: "Historical", Summary: "Imported", Severity: "minor", Status: "resolved", Affects: []string{"api"}, StartedAtUnix: 10, ResolvedAtUnix: 20, CreatedAtUnix: 10}
	if got := apply(t, cell, imported).Transitions; len(got) != 0 {
		t.Fatalf("import emitted transitions: %#v", got)
	}
	importedUpdate := command("incident/import/update", UpdateIncident, 109)
	importedUpdate.ImportMode = true
	importedUpdate.Update = &IncidentUpdate{ID: "imported/resolved", IncidentID: "imported", AtUnix: 20, Label: "RESOLVED", Body: "Historical resolution.", Author: "operator"}
	if got := apply(t, cell, importedUpdate).Transitions; len(got) != 0 {
		t.Fatalf("imported timeline emitted transitions: %#v", got)
	}
	restarted, err := openOwner(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if got := restarted.projection(110); len(got.Incidents) != 1 || got.Incidents[0].ID != "imported" || len(got.IncidentUpdates) != 1 {
		t.Fatalf("lifecycle replay lost imported state: %#v", got)
	}
}

func TestSweepReturnsDeterministicCountersTransitionsAndRestartResult(t *testing.T) {
	store := &memoryEventStore{}
	cell, err := openOwner(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	component := command("component", UpsertComponent, 100)
	component.Component = &Component{ID: "api", Name: "API", Kind: "service"}
	apply(t, cell, component)
	source := command("source", UpsertSource, 100)
	source.Source = &Source{ID: "self", Name: "Self", Kind: SourcePush, Weight: 1, Trusted: true, DefaultTTL: seconds(30)}
	apply(t, cell, source)
	down := command("down/import", AppendObservation, 101)
	down.ImportMode = true
	down.Observation = &Observation{ID: "down", SourceID: "self", ComponentID: "api", Signal: SignalDown, ObservedAtUnix: 101}
	if got := apply(t, cell, down).Transitions; len(got) != 0 {
		t.Fatalf("import observation emitted transitions: %#v", got)
	}
	sweep := command("sweep/1", SweepReconcile, 102)
	first := apply(t, cell, sweep)
	if first.Sweep == nil || first.Sweep.Components != 1 || first.Sweep.Declared != 1 || len(first.Sweep.Transitions) != 1 || first.Sweep.Transitions[0].State != "incident.opened" {
		t.Fatalf("sweep result = %#v", first.Sweep)
	}
	if len(first.Transitions) != 1 || first.Transitions[0].Kind != "incident.opened" {
		t.Fatalf("sweep domain transitions = %#v", first.Transitions)
	}
	restarted, err := openOwner(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := apply(t, restarted, sweep)
	if !duplicate.Deduped {
		t.Fatal("replayed sweep command was not deduplicated")
	}
	first.Deduped = true
	if !reflect.DeepEqual(first, duplicate) {
		t.Fatalf("restart changed sweep result:\nfirst=%#v\nduplicate=%#v", first, duplicate)
	}
	okObservation := command("ok/import", AppendObservation, 103)
	okObservation.ImportMode = true
	okObservation.Observation = &Observation{ID: "ok", SourceID: "self", ComponentID: "api", Signal: SignalOK, ObservedAtUnix: 103}
	apply(t, restarted, okObservation)
	recovery := command("sweep/2", SweepReconcile, 104)
	recovered := apply(t, restarted, recovery)
	if recovered.Sweep == nil || recovered.Sweep.Declared != 0 || len(recovered.Transitions) != 1 || recovered.Transitions[0].Kind != "incident.resolved" {
		t.Fatalf("recovery sweep result = %#v", recovered)
	}
}

func TestOwnerRejectsDanglingReferencesAndUnsafeKinds(t *testing.T) {
	cell, err := openOwner(context.Background(), &memoryEventStore{})
	if err != nil {
		t.Fatal(err)
	}
	root := command("root", UpsertComponent, 100)
	root.Component = &Component{ID: "root", Name: "Root", Kind: "organization"}
	apply(t, cell, root)
	incident := command("bad/incident", OpenIncident, 101)
	incident.Incident = &Incident{ID: "bad", Title: "Bad", Summary: "Bad", Severity: "major", Affects: []string{"root"}}
	if _, err := cell.apply(context.Background(), incident); err == nil {
		t.Fatal("expected non-leaf incident rejection")
	}
	maintenance := command("bad/maintenance", ScheduleMaintenance, 101)
	maintenance.Maintenance = &Maintenance{ID: "bad", Title: "Bad", ScheduledStartUnix: 200, ScheduledEndUnix: 300, Affects: []string{"missing"}}
	if _, err := cell.apply(context.Background(), maintenance); err == nil {
		t.Fatal("expected dangling maintenance reference rejection")
	}
	source := command("bad/source", UpsertSource, 101)
	source.Source = &Source{ID: "bad", Name: "Bad", Kind: SourceKind("not safe!"), Weight: 1}
	if _, err := cell.apply(context.Background(), source); err == nil {
		t.Fatal("expected unsafe source kind rejection")
	}
	if got := cell.projection(101); got.Revision != 1 || len(got.Incidents) != 0 || len(got.Maintenance) != 0 || len(got.Sources) != 0 {
		t.Fatalf("rejected mutations leaked state: %#v", got)
	}
}

func TestIngestAtomicallyUpdatesUptimeAndObservationAcrossReplay(t *testing.T) {
	store := &memoryEventStore{}
	cell, err := openOwner(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	component := command("component", UpsertComponent, 100)
	component.Component = &Component{ID: "api", Name: "API", Kind: "service"}
	apply(t, cell, component)
	source := command("source", UpsertSource, 100)
	source.Source = &Source{ID: "vendor", Name: "Vendor", Kind: SourceKind("vendor.v1"), Weight: 1}
	apply(t, cell, source)
	mapping := command("mapping", MapSourceTarget, 100)
	mapping.Mapping = &SourceTargetMapping{ID: "map", SourceID: "vendor", RawLabel: "api-vendor", ComponentID: "api"}
	apply(t, cell, mapping)

	uptime := []UptimeDay{{Date: "2026-07-26", Status: "out"}}
	ingest := command("ingest/atomic", IngestObservation, 101)
	ingest.Ingest = &IngestRequest{ObservationID: "obs/atomic", SourceID: "vendor", RawLabel: "api-vendor", Signal: SignalDown}
	ingest.ComponentPatch = &ComponentPatch{ID: "api", Uptime90D: &uptime}
	apply(t, cell, ingest)
	if len(store.events) != 4 {
		t.Fatalf("atomic ingest persisted %d events, want exactly one new event", len(store.events))
	}
	got, err := cell.query(Query{Version: ContractVersion, ComponentID: "api", IncludeObservations: true, AtUnix: 101})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Observations) != 1 || !reflect.DeepEqual(got.Components[0].Component.Uptime90D, uptime) {
		t.Fatalf("atomic ingest projection = %#v", got)
	}

	badUptime := []UptimeDay{{Date: "2026-07-26", Status: "ok"}}
	rejected := command("ingest/rejected", IngestObservation, 102)
	rejected.Ingest = &IngestRequest{ObservationID: "obs/rejected", SourceID: "vendor", RawLabel: "api-vendor", Signal: Signal("invalid")}
	rejected.ComponentPatch = &ComponentPatch{ID: "api", Uptime90D: &badUptime}
	if _, err := cell.apply(context.Background(), rejected); err == nil {
		t.Fatal("expected invalid atomic ingest rejection")
	}
	if len(store.events) != 4 {
		t.Fatalf("rejected atomic ingest persisted an event: %d", len(store.events))
	}
	if current := cell.projection(102).Components[0].Component.Uptime90D; !reflect.DeepEqual(current, uptime) {
		t.Fatalf("rejected observation leaked uptime patch: %#v", current)
	}

	restarted, err := openOwner(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := restarted.query(Query{Version: ContractVersion, ComponentID: "api", IncludeObservations: true, AtUnix: 101})
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed.Observations) != 1 || !reflect.DeepEqual(replayed.Components[0].Component.Uptime90D, uptime) {
		t.Fatalf("atomic ingest replay = %#v", replayed)
	}
	duplicate := apply(t, restarted, ingest)
	if !duplicate.Deduped || len(store.events) != 4 {
		t.Fatalf("atomic ingest replay was not idempotent: %#v events=%d", duplicate, len(store.events))
	}
	direct := command("ingest/canonical", IngestObservation, 103)
	direct.Ingest = &IngestRequest{ObservationID: "obs/canonical", SourceID: "vendor", ComponentID: "api", Signal: SignalOK}
	if _, err := restarted.apply(context.Background(), direct); err == nil {
		t.Fatal("expected direct target capability rejection")
	}
	allowDirect := true
	enableDirect := command("source/direct-targets", EditSource, 103)
	enableDirect.SourcePatch = &SourcePatch{ID: "vendor", DirectTargets: &allowDirect}
	apply(t, restarted, enableDirect)
	if result := apply(t, restarted, direct); result.Evaluation == nil || result.Evaluation.ComponentID != "api" {
		t.Fatalf("canonical-component ingest result = %#v", result)
	}
	ambiguous := command("ingest/ambiguous", IngestObservation, 104)
	ambiguous.Ingest = &IngestRequest{ObservationID: "obs/ambiguous", SourceID: "vendor", RawLabel: "api-vendor", ComponentID: "api", Signal: SignalOK}
	if _, err := restarted.apply(context.Background(), ambiguous); err == nil {
		t.Fatal("expected ambiguous target form rejection")
	}
	restartedAgain, err := openOwner(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if !restartedAgain.projection(104).Sources[0].DirectTargets {
		t.Fatal("direct target capability was not durable across replay")
	}
	if duplicateDirect := apply(t, restartedAgain, direct); !duplicateDirect.Deduped {
		t.Fatalf("direct ingest was not idempotent after replay: %#v", duplicateDirect)
	}
}
