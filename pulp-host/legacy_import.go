package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// legacyImportRow is produced only by a privileged host adapter. Identity is
// the canonical, non-secret material used for stable IDs and reconciliation;
// Payload may contain private migration data and is never persisted by this
// coordinator or included in an error.
type legacyImportRow struct {
	Cursor   string
	SortKey  string
	Entity   string
	LegacyID string
	Identity []byte
	Payload  any
}

type legacyImportBatch struct {
	Rows []legacyImportRow
	Done bool
}

type legacyImportRowSource interface {
	ReadAfter(context.Context, string, int) (legacyImportBatch, error)
}

type legacyImportEnvelope struct {
	ImportID string
	Entity   string
	LegacyID string
	Payload  any
}

type legacyImportReceipt struct {
	ImportID  string
	Applied   bool
	Unchanged bool
}

type legacyImportDestination interface {
	ApplyLegacyImport(context.Context, legacyImportEnvelope) (legacyImportReceipt, error)
}

type legacyImportCheckpoint struct {
	Migration    string
	Cursor       string
	LastSortKey  string
	LastLegacyID string
	Applied      uint64
	Unchanged    uint64
	Digest       string
	Completed    bool
}

type legacyImportCheckpointStore interface {
	Load(context.Context, string) (legacyImportCheckpoint, error)
	Save(context.Context, legacyImportCheckpoint) error
}

type legacyImportSummary struct {
	Migration string
	Applied   uint64
	Unchanged uint64
	Digest    string
}

type legacyImportInvariantVerifier interface {
	VerifyLegacyImport(context.Context, legacyImportSummary) error
}

type legacyImportService struct {
	source      legacyImportRowSource
	destination legacyImportDestination
	checkpoints legacyImportCheckpointStore
	verifier    legacyImportInvariantVerifier
	batchSize   int
}

// legacyMonitorSourceDirectTargets is intentionally exact. The legacy schema
// has no capability column; only the canonical active Status Prober is allowed
// to address components directly after migration.
func legacyMonitorSourceDirectTargets(name, kind string, revoked bool) bool {
	return !revoked && name == "Status Prober" && kind == "probe"
}

func newLegacyImportService(
	source legacyImportRowSource,
	destination legacyImportDestination,
	checkpoints legacyImportCheckpointStore,
	verifier legacyImportInvariantVerifier,
	batchSize int,
) (*legacyImportService, error) {
	if source == nil || destination == nil || checkpoints == nil || verifier == nil {
		return nil, errors.New("legacy import source, destination, checkpoint store, and verifier are required")
	}
	if batchSize <= 0 || batchSize > 1000 {
		return nil, errors.New("legacy import batch size must be between 1 and 1000")
	}
	return &legacyImportService{
		source: source, destination: destination, checkpoints: checkpoints, verifier: verifier, batchSize: batchSize,
	}, nil
}

// Run resumes after the last owner-acknowledged row. A checkpoint is advanced
// only after the destination returns a receipt bound to the exact stable
// import ID. Completion is persisted only after destination invariants pass.
func (s *legacyImportService) Run(ctx context.Context, migration string) (legacyImportSummary, error) {
	if strings.TrimSpace(migration) == "" {
		return legacyImportSummary{}, errors.New("legacy import migration name is required")
	}
	checkpoint, err := s.checkpoints.Load(ctx, migration)
	if err != nil {
		return legacyImportSummary{}, fmt.Errorf("load legacy import checkpoint: %w", err)
	}
	if checkpoint.Migration == "" {
		checkpoint.Migration = migration
	}
	if checkpoint.Migration != migration {
		return legacyImportSummary{}, errors.New("legacy import checkpoint is bound to another migration")
	}
	if checkpoint.Completed {
		return checkpoint.summary(), nil
	}

	for {
		batch, err := s.source.ReadAfter(ctx, checkpoint.Cursor, s.batchSize)
		if err != nil {
			return checkpoint.summary(), fmt.Errorf("read legacy import batch: %w", err)
		}
		if len(batch.Rows) == 0 && !batch.Done {
			return checkpoint.summary(), errors.New("legacy import source returned an empty non-terminal batch")
		}
		for _, row := range batch.Rows {
			if err := validateLegacyImportRow(row, checkpoint); err != nil {
				return checkpoint.summary(), err
			}
			importID := stableLegacyImportID(row)
			receipt, err := s.destination.ApplyLegacyImport(ctx, legacyImportEnvelope{
				ImportID: importID,
				Entity:   row.Entity,
				LegacyID: row.LegacyID,
				Payload:  row.Payload,
			})
			if err != nil {
				return checkpoint.summary(), fmt.Errorf("apply legacy import row: %w", err)
			}
			if receipt.ImportID != importID {
				return checkpoint.summary(), errors.New("legacy import receipt does not match requested import ID")
			}
			if !receipt.Applied && !receipt.Unchanged {
				return checkpoint.summary(), errors.New("legacy import receipt did not acknowledge the row")
			}
			if receipt.Applied {
				checkpoint.Applied++
			} else {
				checkpoint.Unchanged++
			}
			checkpoint.Cursor = row.Cursor
			checkpoint.LastSortKey = row.SortKey
			checkpoint.LastLegacyID = row.LegacyID
			checkpoint.Digest = advanceLegacyImportDigest(checkpoint.Digest, importID)
			if err := s.checkpoints.Save(ctx, checkpoint); err != nil {
				return checkpoint.summary(), fmt.Errorf("save legacy import checkpoint: %w", err)
			}
		}
		if !batch.Done {
			continue
		}
		summary := checkpoint.summary()
		if err := s.verifier.VerifyLegacyImport(ctx, summary); err != nil {
			return summary, fmt.Errorf("verify legacy import invariants: %w", err)
		}
		checkpoint.Completed = true
		if err := s.checkpoints.Save(ctx, checkpoint); err != nil {
			return summary, fmt.Errorf("save completed legacy import checkpoint: %w", err)
		}
		return summary, nil
	}
}

func (c legacyImportCheckpoint) summary() legacyImportSummary {
	return legacyImportSummary{
		Migration: c.Migration,
		Applied:   c.Applied,
		Unchanged: c.Unchanged,
		Digest:    c.Digest,
	}
}

func validateLegacyImportRow(row legacyImportRow, checkpoint legacyImportCheckpoint) error {
	if row.Cursor == "" || row.SortKey == "" || row.Entity == "" || row.LegacyID == "" || len(row.Identity) == 0 {
		return errors.New("legacy import row is missing stable identity or ordering fields")
	}
	if row.SortKey < checkpoint.LastSortKey ||
		(row.SortKey == checkpoint.LastSortKey && row.LegacyID <= checkpoint.LastLegacyID) {
		return errors.New("legacy import source returned rows out of stable order")
	}
	return nil
}

func stableLegacyImportID(row legacyImportRow) string {
	sum := sha256.Sum256(row.Identity)
	return "bananapulse/import/v1/" + url.PathEscape(row.Entity) + "/" +
		url.PathEscape(row.LegacyID) + "/" + hex.EncodeToString(sum[:])
}

func advanceLegacyImportDigest(previous, importID string) string {
	sum := sha256.Sum256([]byte(previous + "\x00" + importID))
	return hex.EncodeToString(sum[:])
}

type sqliteLegacyImportCheckpointStore struct {
	db *sql.DB
}

func newSQLiteLegacyImportCheckpointStore(db *sql.DB) (*sqliteLegacyImportCheckpointStore, error) {
	if db == nil {
		return nil, errors.New("legacy import checkpoint database is required")
	}
	store := &sqliteLegacyImportCheckpointStore{db: db}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS pulp_legacy_import_checkpoints (
			migration TEXT PRIMARY KEY,
			cursor TEXT NOT NULL,
			last_sort_key TEXT NOT NULL,
			last_legacy_id TEXT NOT NULL,
			applied INTEGER NOT NULL,
			unchanged INTEGER NOT NULL,
			digest TEXT NOT NULL,
			completed INTEGER NOT NULL
		)`); err != nil {
		return nil, fmt.Errorf("migrate legacy import checkpoints: %w", err)
	}
	return store, nil
}

func (s *sqliteLegacyImportCheckpointStore) Load(ctx context.Context, migration string) (legacyImportCheckpoint, error) {
	checkpoint := legacyImportCheckpoint{Migration: migration}
	var completed int
	err := s.db.QueryRowContext(ctx, `
		SELECT cursor,last_sort_key,last_legacy_id,applied,unchanged,digest,completed
		FROM pulp_legacy_import_checkpoints WHERE migration = ?`, migration,
	).Scan(
		&checkpoint.Cursor,
		&checkpoint.LastSortKey,
		&checkpoint.LastLegacyID,
		&checkpoint.Applied,
		&checkpoint.Unchanged,
		&checkpoint.Digest,
		&completed,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return checkpoint, nil
	}
	if err != nil {
		return legacyImportCheckpoint{}, err
	}
	checkpoint.Completed = completed != 0
	return checkpoint, nil
}

func (s *sqliteLegacyImportCheckpointStore) Save(ctx context.Context, checkpoint legacyImportCheckpoint) error {
	completed := 0
	if checkpoint.Completed {
		completed = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO pulp_legacy_import_checkpoints(
			migration,cursor,last_sort_key,last_legacy_id,applied,unchanged,digest,completed
		) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(migration) DO UPDATE SET
			cursor=excluded.cursor,
			last_sort_key=excluded.last_sort_key,
			last_legacy_id=excluded.last_legacy_id,
			applied=excluded.applied,
			unchanged=excluded.unchanged,
			digest=excluded.digest,
			completed=excluded.completed`,
		checkpoint.Migration,
		checkpoint.Cursor,
		checkpoint.LastSortKey,
		checkpoint.LastLegacyID,
		checkpoint.Applied,
		checkpoint.Unchanged,
		checkpoint.Digest,
		completed,
	)
	return err
}
