package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	legacyImportEnabledEnv   = "PULP_LEGACY_IMPORT_ENABLED"
	legacyImportMigrationEnv = "PULP_LEGACY_IMPORT_MIGRATION"
	legacyImportFenceEnv     = "PULP_LEGACY_IMPORT_FENCE"
	legacyImportSourceDSNEnv = "PULP_LEGACY_IMPORT_SOURCE_DSN"
	legacyImportBatchSizeEnv = "PULP_LEGACY_IMPORT_BATCH_SIZE"
	legacyImportTimeoutEnv   = "PULP_LEGACY_IMPORT_TIMEOUT"
	legacyImportReverifyEnv  = "PULP_LEGACY_IMPORT_REVERIFY_COMPLETED"

	defaultLegacyImportMigration = "bananapulse-postgres-v1"
	defaultLegacyImportBatchSize = 100
	defaultLegacyImportTimeout   = 30 * time.Minute
	legacyImportCheckpointFile   = "pulp-host-legacy-import.sqlite"
)

type legacyImportStartupConfig struct {
	Enabled   bool
	Migration string
	Fence     string
	SourceDSN string
	BatchSize int
	Timeout   time.Duration
	Reverify  bool
}

func legacyImportStartupConfigFromEnv() legacyImportStartupConfig {
	config := legacyImportStartupConfig{
		Enabled:   bridgeEnvEnabled(legacyImportEnabledEnv),
		Migration: strings.TrimSpace(os.Getenv(legacyImportMigrationEnv)),
		Fence:     strings.TrimSpace(os.Getenv(legacyImportFenceEnv)),
		SourceDSN: strings.TrimSpace(os.Getenv(legacyImportSourceDSNEnv)),
		BatchSize: defaultLegacyImportBatchSize,
		Timeout:   defaultLegacyImportTimeout,
		Reverify:  bridgeEnvEnabled(legacyImportReverifyEnv),
	}
	if config.Migration == "" {
		config.Migration = defaultLegacyImportMigration
	}
	if value := strings.TrimSpace(os.Getenv(legacyImportBatchSizeEnv)); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			config.BatchSize = parsed
		} else {
			config.BatchSize = 0
		}
	}
	if value := strings.TrimSpace(os.Getenv(legacyImportTimeoutEnv)); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil {
			config.Timeout = parsed
		} else {
			config.Timeout = 0
		}
	}
	return config
}

func legacyImportFenceFor(migration string) string {
	digest := sha256.Sum256([]byte("bananapulse/legacy-import/startup/v1\x00" + migration))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func (config legacyImportStartupConfig) validate() error {
	if !config.Enabled {
		return nil
	}
	if config.Migration == "" || len(config.Migration) > 128 ||
		config.Migration != strings.TrimSpace(config.Migration) ||
		strings.IndexFunc(config.Migration, func(r rune) bool {
			return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-')
		}) >= 0 {
		return errors.New("legacy import migration identity is invalid")
	}
	if config.Fence != legacyImportFenceFor(config.Migration) {
		return errors.New("legacy import startup fence does not match the migration")
	}
	if config.SourceDSN == "" {
		return errors.New("legacy import source is required when startup import is enabled")
	}
	if config.BatchSize <= 0 || config.BatchSize > 1000 {
		return errors.New("legacy import batch size must be between 1 and 1000")
	}
	if config.Timeout <= 0 || config.Timeout > 24*time.Hour {
		return errors.New("legacy import timeout must be between 1ns and 24h")
	}
	return nil
}

type legacyImportInvariantCounts struct {
	Components              uint64
	ArchivedComponents      uint64
	Sources                 uint64
	RevokedSources          uint64
	Mappings                uint64
	Observations            uint64
	Incidents               uint64
	IncidentUpdates         uint64
	Maintenance             uint64
	Subscribers             uint64
	PendingSubscribers      uint64
	ConfirmedSubscribers    uint64
	APITokens               uint64
	ActiveAPITokens         uint64
	ActiveSourceCredentials uint64
}

func (expected legacyImportInvariantCounts) operationCount() uint64 {
	return expected.Components + expected.Sources + expected.Mappings +
		expected.Observations + expected.Incidents + expected.IncidentUpdates +
		expected.Maintenance + expected.ArchivedComponents +
		expected.RevokedSources + expected.Subscribers + expected.APITokens
}

func loadLegacyImportInvariantCounts(ctx context.Context, queryer legacySQLQueryer, at time.Time) (legacyImportInvariantCounts, error) {
	if queryer == nil {
		return legacyImportInvariantCounts{}, errors.New("legacy invariant source is required")
	}
	var counts legacyImportInvariantCounts
	row := func(query string, target *uint64, args ...any) error {
		rows, err := queryer.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				return err
			}
			return errors.New("legacy invariant count returned no row")
		}
		if err := rows.Scan(target); err != nil {
			return err
		}
		if rows.Next() {
			return errors.New("legacy invariant count returned multiple rows")
		}
		return rows.Err()
	}
	queries := []struct {
		sql    string
		target *uint64
		args   []any
	}{
		{`SELECT COUNT(*) FROM components`, &counts.Components, nil},
		{`SELECT COUNT(*) FROM components WHERE archived_at IS NOT NULL`, &counts.ArchivedComponents, nil},
		{`SELECT COUNT(*) FROM sources`, &counts.Sources, nil},
		{`SELECT COUNT(*) FROM sources WHERE revoked_at IS NOT NULL`, &counts.RevokedSources, nil},
		{`SELECT COUNT(*) FROM source_target_map`, &counts.Mappings, nil},
		{`SELECT COUNT(*) FROM observations`, &counts.Observations, nil},
		{`SELECT COUNT(*) FROM incidents`, &counts.Incidents, nil},
		{`SELECT COUNT(*) FROM incident_timeline`, &counts.IncidentUpdates, nil},
		{`SELECT COUNT(*) FROM maintenance`, &counts.Maintenance, nil},
		{`SELECT COUNT(*) FROM subscribers`, &counts.Subscribers, nil},
		{`SELECT COUNT(*) FROM subscribers WHERE confirmed_at IS NULL`, &counts.PendingSubscribers, nil},
		{`SELECT COUNT(*) FROM subscribers WHERE confirmed_at IS NOT NULL`, &counts.ConfirmedSubscribers, nil},
		{`SELECT COUNT(*) FROM api_tokens`, &counts.APITokens, nil},
		{`SELECT COUNT(*) FROM api_tokens WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at > $1)`, &counts.ActiveAPITokens, []any{at}},
		{`SELECT COUNT(*) FROM sources WHERE revoked_at IS NULL`, &counts.ActiveSourceCredentials, nil},
	}
	for _, query := range queries {
		if err := row(query.sql, query.target, query.args...); err != nil {
			return legacyImportInvariantCounts{}, fmt.Errorf("read legacy invariant count: %w", err)
		}
	}
	return counts, nil
}

func acquireLegacyImportFence(ctx context.Context, queryer legacySQLQueryer, migration string) error {
	digest := sha256.Sum256([]byte("bananapulse/legacy-import/lock/v1\x00" + migration))
	lockID := int64(binary.BigEndian.Uint64(digest[:8]))
	rows, err := queryer.QueryContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, lockID)
	if err != nil {
		return errors.New("acquire legacy import source fence")
	}
	defer rows.Close()
	var acquired bool
	if !rows.Next() {
		return errors.New("legacy import source fence returned no row")
	}
	if err := rows.Scan(&acquired); err != nil {
		return errors.New("read legacy import source fence")
	}
	if rows.Next() {
		return errors.New("legacy import source fence returned multiple rows")
	}
	if err := rows.Err(); err != nil {
		return errors.New("read legacy import source fence")
	}
	if !acquired {
		return errors.New("another host owns the legacy import source fence")
	}
	return nil
}

type legacyMonitorInvariantProjection struct {
	Components []struct {
		Component struct {
			Archived bool `msgpack:"archived"`
		} `msgpack:"component"`
	} `msgpack:"components"`
	Sources []struct {
		Revoked bool `msgpack:"revoked"`
	} `msgpack:"sources"`
	Mappings        []any `msgpack:"mappings"`
	Observations    []any `msgpack:"observations"`
	Incidents       []any `msgpack:"incidents"`
	IncidentUpdates []any `msgpack:"incident_updates"`
	Maintenance     []any `msgpack:"maintenance"`
}

type legacySubscriberInvariantProjection struct {
	PendingCount      int `msgpack:"pending_count"`
	ConfirmedCount    int `msgpack:"confirmed_count"`
	UnsubscribedCount int `msgpack:"unsubscribed_count"`
}

type legacyAuthInvariantProjection struct {
	ActiveAPITokenCount         int `msgpack:"active_api_token_count"`
	ActiveSourceCredentialCount int `msgpack:"active_source_credential_count"`
}

type bananapulseLegacyImportVerifier struct {
	client    sourceLifecycleClient
	expected  legacyImportInvariantCounts
	migration string
	at        time.Time
}

func (verifier *bananapulseLegacyImportVerifier) VerifyLegacyImport(ctx context.Context, summary legacyImportSummary) error {
	if verifier == nil || verifier.client == nil {
		return errors.New("legacy invariant destination is required")
	}
	if summary.Migration != verifier.migration ||
		summary.Applied+summary.Unchanged != verifier.expected.operationCount() {
		return errors.New("legacy import operation count does not match source snapshot")
	}
	if verifier.expected.operationCount() > 0 && summary.Digest == "" {
		return errors.New("legacy import digest is missing")
	}
	var monitor legacyMonitorInvariantProjection
	if err := verifier.client.callRaw(ctx, eventMonitorQuery, bridgeMonitorQuery{
		Version: "monitor.v1", IncludeArchived: true, IncludeObservations: true,
		AtUnix: verifier.at.Unix(),
	}, &monitor); err != nil {
		return fmt.Errorf("verify monitor projection: %w", err)
	}
	archived := 0
	for _, component := range monitor.Components {
		if component.Component.Archived {
			archived++
		}
	}
	revoked := 0
	for _, source := range monitor.Sources {
		if source.Revoked {
			revoked++
		}
	}
	actualMonitor := []uint64{
		uint64(len(monitor.Components)), uint64(archived),
		uint64(len(monitor.Sources)), uint64(revoked),
		uint64(len(monitor.Mappings)), uint64(len(monitor.Observations)),
		uint64(len(monitor.Incidents)), uint64(len(monitor.IncidentUpdates)),
		uint64(len(monitor.Maintenance)),
	}
	expectedMonitor := []uint64{
		verifier.expected.Components, verifier.expected.ArchivedComponents,
		verifier.expected.Sources, verifier.expected.RevokedSources,
		verifier.expected.Mappings, verifier.expected.Observations,
		verifier.expected.Incidents, verifier.expected.IncidentUpdates,
		verifier.expected.Maintenance,
	}
	for index := range actualMonitor {
		if actualMonitor[index] != expectedMonitor[index] {
			return errors.New("legacy monitor invariants do not match source snapshot")
		}
	}
	var subscribers legacySubscriberInvariantProjection
	if err := verifier.client.callRaw(ctx, eventSubscriberProjection, nil, &subscribers); err != nil {
		return fmt.Errorf("verify subscriber projection: %w", err)
	}
	if uint64(subscribers.PendingCount) != verifier.expected.PendingSubscribers ||
		uint64(subscribers.ConfirmedCount) != verifier.expected.ConfirmedSubscribers ||
		subscribers.UnsubscribedCount != 0 {
		return errors.New("legacy subscriber invariants do not match source snapshot")
	}
	var auth legacyAuthInvariantProjection
	if err := verifier.client.callProviderRaw(ctx, authOwnerCell, providerAuthProjection, bridgeAuthProjectionRequest{
		Version: "bananapulse.auth/v1", At: verifier.at,
	}, &auth); err != nil {
		return fmt.Errorf("verify auth projection: %w", err)
	}
	if uint64(auth.ActiveAPITokenCount) != verifier.expected.ActiveAPITokens ||
		uint64(auth.ActiveSourceCredentialCount) != verifier.expected.ActiveSourceCredentials {
		return errors.New("legacy auth invariants do not match source snapshot")
	}
	return nil
}

func runLegacyImportAtStartup(
	parent context.Context,
	config legacyImportStartupConfig,
	storageRoot string,
	client sourceLifecycleClient,
) (legacyImportSummary, error) {
	if err := config.validate(); err != nil {
		return legacyImportSummary{}, err
	}
	if !config.Enabled {
		return legacyImportSummary{}, nil
	}
	ctx, cancel := context.WithTimeout(parent, config.Timeout)
	defer cancel()

	if err := os.MkdirAll(storageRoot, 0o700); err != nil {
		return legacyImportSummary{}, fmt.Errorf("create legacy checkpoint directory: %w", err)
	}
	checkpointDB, err := sql.Open("sqlite", filepath.Join(storageRoot, legacyImportCheckpointFile))
	if err != nil {
		return legacyImportSummary{}, fmt.Errorf("open legacy import checkpoints: %w", err)
	}
	defer checkpointDB.Close()
	checkpointDB.SetMaxOpenConns(1)
	checkpointDB.SetMaxIdleConns(1)
	checkpoints, err := newSQLiteLegacyImportCheckpointStore(checkpointDB)
	if err != nil {
		return legacyImportSummary{}, err
	}
	checkpoint, err := checkpoints.Load(ctx, config.Migration)
	if err != nil {
		return legacyImportSummary{}, fmt.Errorf("load legacy import checkpoint: %w", err)
	}
	if checkpoint.Completed && !config.Reverify {
		return checkpoint.summary(), nil
	}

	sourceDB, err := sql.Open("pgx", config.SourceDSN)
	if err != nil {
		return legacyImportSummary{}, errors.New("open legacy import source")
	}
	defer sourceDB.Close()
	sourceDB.SetMaxOpenConns(1)
	sourceDB.SetMaxIdleConns(1)
	if err := sourceDB.PingContext(ctx); err != nil {
		return legacyImportSummary{}, errors.New("connect legacy import source")
	}
	tx, err := sourceDB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return legacyImportSummary{}, errors.New("begin read-only legacy import snapshot")
	}
	defer tx.Rollback()
	if err := acquireLegacyImportFence(ctx, tx, config.Migration); err != nil {
		return legacyImportSummary{}, err
	}

	at := time.Now().UTC()
	expected, err := loadLegacyImportInvariantCounts(ctx, tx, at)
	if err != nil {
		return legacyImportSummary{}, err
	}
	verifier := &bananapulseLegacyImportVerifier{
		client: client, expected: expected, migration: config.Migration, at: at,
	}
	service, err := newBananapulseLegacyImportServiceFromQueryer(
		tx, checkpointDB, client, verifier, config.BatchSize,
	)
	if err != nil {
		return legacyImportSummary{}, err
	}
	summary, err := service.Run(ctx, config.Migration)
	if err != nil {
		return summary, err
	}
	// A completed checkpoint skips all owner commands. Operators can explicitly
	// request a source-backed audit without turning the one-time import into a
	// permanent dependency on the legacy database.
	if checkpoint.Completed {
		if err := verifier.VerifyLegacyImport(ctx, summary); err != nil {
			return summary, fmt.Errorf("re-verify completed legacy import: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return summary, errors.New("finish read-only legacy import snapshot")
	}
	return summary, nil
}
