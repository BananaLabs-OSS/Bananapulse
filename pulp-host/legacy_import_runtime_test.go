package main

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func validLegacyImportStartupConfig() legacyImportStartupConfig {
	migration := "bananapulse-postgres-v1"
	return legacyImportStartupConfig{
		Enabled:   true,
		Migration: migration,
		Fence:     legacyImportFenceFor(migration),
		SourceDSN: "postgres://legacy.invalid/bananapulse",
		BatchSize: 100,
		Timeout:   time.Minute,
	}
}

func TestLegacyImportStartupConfigIsDisabledAndFailClosedByDefault(t *testing.T) {
	t.Setenv(legacyImportEnabledEnv, "")
	t.Setenv(legacyImportMigrationEnv, "")
	t.Setenv(legacyImportFenceEnv, "")
	t.Setenv(legacyImportSourceDSNEnv, "")
	t.Setenv(legacyImportBatchSizeEnv, "")
	t.Setenv(legacyImportTimeoutEnv, "")
	t.Setenv(legacyImportReverifyEnv, "")

	config := legacyImportStartupConfigFromEnv()
	if config.Enabled || config.Reverify {
		t.Fatalf("default config enabled a legacy operation: %#v", config)
	}
	if err := config.validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := runLegacyImportAtStartup(context.Background(), config, t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyImportStartupConfigRequiresExactFenceAndBoundedInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*legacyImportStartupConfig)
	}{
		{name: "migration", mutate: func(c *legacyImportStartupConfig) { c.Migration = "../other" }},
		{name: "fence", mutate: func(c *legacyImportStartupConfig) { c.Fence = "sha256:wrong" }},
		{name: "source", mutate: func(c *legacyImportStartupConfig) { c.SourceDSN = "" }},
		{name: "batch zero", mutate: func(c *legacyImportStartupConfig) { c.BatchSize = 0 }},
		{name: "batch large", mutate: func(c *legacyImportStartupConfig) { c.BatchSize = 1001 }},
		{name: "timeout zero", mutate: func(c *legacyImportStartupConfig) { c.Timeout = 0 }},
		{name: "timeout large", mutate: func(c *legacyImportStartupConfig) { c.Timeout = 24*time.Hour + time.Nanosecond }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validLegacyImportStartupConfig()
			test.mutate(&config)
			if err := config.validate(); err == nil {
				t.Fatal("unsafe startup config was accepted")
			}
		})
	}
	if err := validLegacyImportStartupConfig().validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCompletedLegacyImportRestartDoesNotReachSourceOrOwners(t *testing.T) {
	storageRoot := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(storageRoot, legacyImportCheckpointFile))
	if err != nil {
		t.Fatal(err)
	}
	store, err := newSQLiteLegacyImportCheckpointStore(db)
	if err != nil {
		t.Fatal(err)
	}
	want := legacyImportCheckpoint{
		Migration: defaultLegacyImportMigration,
		Applied:   12,
		Digest:    "completed-digest",
		Completed: true,
	}
	if err := store.Save(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	config := validLegacyImportStartupConfig()
	config.SourceDSN = "this is deliberately not a valid PostgreSQL DSN"
	summary, err := runLegacyImportAtStartup(context.Background(), config, storageRoot, nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary != want.summary() {
		t.Fatalf("summary = %#v, want %#v", summary, want.summary())
	}
}

func TestLegacyImportConnectionErrorsNeverExposeSourceDSN(t *testing.T) {
	config := validLegacyImportStartupConfig()
	config.SourceDSN = "postgres://private-user:private-password@127.0.0.1:1/private"
	_, err := runLegacyImportAtStartup(context.Background(), config, t.TempDir(), nil)
	if err == nil {
		t.Fatal("expected source connection failure")
	}
	if strings.Contains(err.Error(), "private-user") || strings.Contains(err.Error(), "private-password") {
		t.Fatalf("connection error exposed source credentials: %v", err)
	}
}

func TestLegacyImportFenceFailsClosedWhenSourceCannotProvideAdvisoryLock(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "not-postgres.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := acquireLegacyImportFence(context.Background(), db, "legacy-v1"); err == nil {
		t.Fatal("source without PostgreSQL advisory locking was accepted")
	}
}

func TestLoadLegacyImportInvariantCountsUsesCompleteFixedSnapshot(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	schema := []string{
		`CREATE TABLE components (id TEXT, archived_at TIMESTAMP)`,
		`CREATE TABLE sources (id TEXT, revoked_at TIMESTAMP)`,
		`CREATE TABLE source_target_map (id TEXT)`,
		`CREATE TABLE observations (id TEXT)`,
		`CREATE TABLE incidents (id TEXT)`,
		`CREATE TABLE incident_timeline (id TEXT)`,
		`CREATE TABLE maintenance (id TEXT)`,
		`CREATE TABLE subscribers (id TEXT, confirmed_at TIMESTAMP)`,
		`CREATE TABLE api_tokens (id TEXT, revoked_at TIMESTAMP, expires_at TIMESTAMP)`,
	}
	for _, statement := range schema {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	inserts := []string{
		`INSERT INTO components VALUES ('active',NULL),('archived','2026-07-25T00:00:00Z')`,
		`INSERT INTO sources VALUES ('active',NULL),('revoked','2026-07-25T00:00:00Z')`,
		`INSERT INTO source_target_map VALUES ('mapping')`,
		`INSERT INTO observations VALUES ('observation')`,
		`INSERT INTO incidents VALUES ('incident')`,
		`INSERT INTO incident_timeline VALUES ('update')`,
		`INSERT INTO maintenance VALUES ('maintenance')`,
		`INSERT INTO subscribers VALUES ('pending',NULL),('confirmed','2026-07-25T00:00:00Z')`,
		`INSERT INTO api_tokens VALUES ('active',NULL,NULL),('revoked','2026-07-25T00:00:00Z',NULL)`,
	}
	for _, statement := range inserts {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	counts, err := loadLegacyImportInvariantCounts(context.Background(), db, at)
	if err != nil {
		t.Fatal(err)
	}
	want := legacyImportInvariantCounts{
		Components: 2, ArchivedComponents: 1,
		Sources: 2, RevokedSources: 1,
		Mappings: 1, Observations: 1, Incidents: 1, IncidentUpdates: 1, Maintenance: 1,
		Subscribers: 2, PendingSubscribers: 1, ConfirmedSubscribers: 1,
		APITokens: 2, ActiveAPITokens: 1, ActiveSourceCredentials: 1,
	}
	if counts != want {
		t.Fatalf("counts = %#v, want %#v", counts, want)
	}
	if counts.operationCount() != 15 {
		t.Fatalf("operation count = %d, want 15", counts.operationCount())
	}
}

type legacyInvariantTestClient struct {
	expected      legacyImportInvariantCounts
	appCalls      int
	providerCalls int
	failEvent     string
}

func (client *legacyInvariantTestClient) callRaw(_ context.Context, event string, _ any, output any) error {
	client.appCalls++
	if client.failEvent == event {
		return errors.New("projection unavailable")
	}
	switch event {
	case eventMonitorQuery:
		projection := output.(*legacyMonitorInvariantProjection)
		for index := uint64(0); index < client.expected.Components; index++ {
			var component struct {
				Component struct {
					Archived bool `msgpack:"archived"`
				} `msgpack:"component"`
			}
			component.Component.Archived = index < client.expected.ArchivedComponents
			projection.Components = append(projection.Components, component)
		}
		for index := uint64(0); index < client.expected.Sources; index++ {
			var source struct {
				Revoked bool `msgpack:"revoked"`
			}
			source.Revoked = index < client.expected.RevokedSources
			projection.Sources = append(projection.Sources, source)
		}
		projection.Mappings = make([]any, client.expected.Mappings)
		projection.Observations = make([]any, client.expected.Observations)
		projection.Incidents = make([]any, client.expected.Incidents)
		projection.IncidentUpdates = make([]any, client.expected.IncidentUpdates)
		projection.Maintenance = make([]any, client.expected.Maintenance)
	case eventSubscriberProjection:
		projection := output.(*legacySubscriberInvariantProjection)
		projection.PendingCount = int(client.expected.PendingSubscribers)
		projection.ConfirmedCount = int(client.expected.ConfirmedSubscribers)
	default:
		return errors.New("unexpected application projection")
	}
	return nil
}

func (client *legacyInvariantTestClient) callProviderRaw(
	_ context.Context,
	cell string,
	provider string,
	_ any,
	output any,
) error {
	client.providerCalls++
	if cell != authOwnerCell || provider != providerAuthProjection {
		return errors.New("unexpected provider projection")
	}
	projection := output.(*legacyAuthInvariantProjection)
	projection.ActiveAPITokenCount = int(client.expected.ActiveAPITokens)
	projection.ActiveSourceCredentialCount = int(client.expected.ActiveSourceCredentials)
	return nil
}

func TestLegacyImportVerifierChecksEveryOwnerProjection(t *testing.T) {
	expected := legacyImportInvariantCounts{
		Components: 2, ArchivedComponents: 1,
		Sources: 2, RevokedSources: 1,
		Mappings: 1, Observations: 1, Incidents: 1, IncidentUpdates: 1, Maintenance: 1,
		Subscribers: 2, PendingSubscribers: 1, ConfirmedSubscribers: 1,
		APITokens: 2, ActiveAPITokens: 1, ActiveSourceCredentials: 1,
	}
	client := &legacyInvariantTestClient{expected: expected}
	verifier := &bananapulseLegacyImportVerifier{
		client: client, expected: expected, migration: "legacy-v1",
		at: time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
	}
	summary := legacyImportSummary{
		Migration: "legacy-v1", Applied: expected.operationCount(), Digest: "digest",
	}
	if err := verifier.VerifyLegacyImport(context.Background(), summary); err != nil {
		t.Fatal(err)
	}
	if client.appCalls != 2 || client.providerCalls != 1 {
		t.Fatalf("projection calls app=%d provider=%d", client.appCalls, client.providerCalls)
	}

	client.appCalls, client.providerCalls = 0, 0
	summary.Applied--
	if err := verifier.VerifyLegacyImport(context.Background(), summary); err == nil {
		t.Fatal("operation-count mismatch was accepted")
	}
	if client.appCalls != 0 || client.providerCalls != 0 {
		t.Fatal("count mismatch reached destination projections")
	}
}
