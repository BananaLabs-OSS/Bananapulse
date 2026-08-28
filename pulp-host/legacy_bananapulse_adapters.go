package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var legacyBananapulsePhases = []string{
	"component",
	"source",
	"mapping",
	"observation",
	"incident",
	"incident_update",
	"maintenance",
	"component_archive",
	"source_revoke",
	"subscriber",
	"api_token",
}

type legacyPhaseRecord struct {
	SortKey string
	ID      string
	Payload json.RawMessage
}

type legacyPhaseReader interface {
	ReadLegacyPhase(context.Context, string, string, int) ([]legacyPhaseRecord, error)
}

type legacySQLQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// postgresLegacyPhaseReader is an injected, read-only adapter. It never opens
// a connection and never reads configuration or credentials.
type postgresLegacyPhaseReader struct {
	db legacySQLQueryer
}

func newPostgresLegacyPhaseReader(db legacySQLQueryer) (*postgresLegacyPhaseReader, error) {
	if db == nil {
		return nil, errors.New("legacy Postgres connection is required")
	}
	return &postgresLegacyPhaseReader{db: db}, nil
}

func (r *postgresLegacyPhaseReader) ReadLegacyPhase(
	ctx context.Context,
	phase string,
	after string,
	limit int,
) ([]legacyPhaseRecord, error) {
	query, ok := legacyBananapulsePhaseSQL[phase]
	if !ok {
		return nil, fmt.Errorf("unsupported legacy phase %q", phase)
	}
	rows, err := r.db.QueryContext(ctx, query, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]legacyPhaseRecord, 0, limit)
	for rows.Next() {
		var record legacyPhaseRecord
		var payload string
		if err := rows.Scan(&record.SortKey, &record.ID, &payload); err != nil {
			return nil, err
		}
		record.Payload = json.RawMessage(payload)
		records = append(records, record)
	}
	return records, rows.Err()
}

var legacyBananapulsePhaseSQL = map[string]string{
	"component": `
		WITH RECURSIVE tree AS (
			SELECT c.*, 0 AS import_depth FROM components c WHERE c.parent_id IS NULL
			UNION ALL
			SELECT c.*, tree.import_depth + 1 FROM components c JOIN tree ON c.parent_id = tree.id
		), ordered AS (
			SELECT tree.*, lpad(import_depth::text,10,'0') || ':' || id AS import_sort_key FROM tree
		)
		SELECT import_sort_key,id,row_to_json(ordered)::text FROM ordered
		WHERE import_sort_key > $1 ORDER BY import_sort_key,id LIMIT $2`,
	"source": `
		SELECT id,id,row_to_json(t)::text FROM (SELECT * FROM sources) t
		WHERE id > $1 ORDER BY id LIMIT $2`,
	"mapping": `
		SELECT id,id,row_to_json(t)::text FROM (SELECT * FROM source_target_map) t
		WHERE id > $1 ORDER BY id LIMIT $2`,
	"observation": `
		SELECT import_sort_key,id,row_to_json(t)::text FROM (
			SELECT observations.*,
				to_char(observed_at AT TIME ZONE 'UTC','YYYYMMDDHH24MISS.US') || ':' || id AS import_sort_key
			FROM observations
		) t WHERE import_sort_key > $1 ORDER BY import_sort_key,id LIMIT $2`,
	"incident": `
		SELECT id,id,row_to_json(t)::text FROM (SELECT * FROM incidents) t
		WHERE id > $1 ORDER BY id LIMIT $2`,
	"incident_update": `
		SELECT import_sort_key,id,row_to_json(t)::text FROM (
			SELECT incident_timeline.*,
				to_char(at AT TIME ZONE 'UTC','YYYYMMDDHH24MISS.US') || ':' || id AS import_sort_key
			FROM incident_timeline
		) t WHERE import_sort_key > $1 ORDER BY import_sort_key,id LIMIT $2`,
	"maintenance": `
		SELECT id,id,row_to_json(t)::text FROM (SELECT * FROM maintenance) t
		WHERE id > $1 ORDER BY id LIMIT $2`,
	"component_archive": `
		SELECT id,id,row_to_json(t)::text FROM (SELECT * FROM components WHERE archived_at IS NOT NULL) t
		WHERE id > $1 ORDER BY id LIMIT $2`,
	"source_revoke": `
		SELECT id,id,row_to_json(t)::text FROM (SELECT * FROM sources WHERE revoked_at IS NOT NULL) t
		WHERE id > $1 ORDER BY id LIMIT $2`,
	"subscriber": `
		SELECT id,id,row_to_json(t)::text FROM (SELECT * FROM subscribers) t
		WHERE id > $1 ORDER BY id LIMIT $2`,
	"api_token": `
		SELECT id,id,row_to_json(t)::text FROM (SELECT * FROM api_tokens) t
		WHERE id > $1 ORDER BY id LIMIT $2`,
}

type legacyBananapulseRowSource struct {
	reader legacyPhaseReader
}

func newLegacyBananapulseRowSource(reader legacyPhaseReader) (*legacyBananapulseRowSource, error) {
	if reader == nil {
		return nil, errors.New("legacy phase reader is required")
	}
	return &legacyBananapulseRowSource{reader: reader}, nil
}

func (s *legacyBananapulseRowSource) ReadAfter(
	ctx context.Context,
	cursor string,
	limit int,
) (legacyImportBatch, error) {
	phaseIndex, after, err := decodeLegacyBananapulseCursor(cursor)
	if err != nil {
		return legacyImportBatch{}, err
	}
	batch := legacyImportBatch{Rows: make([]legacyImportRow, 0, limit)}
	for phaseIndex < len(legacyBananapulsePhases) && len(batch.Rows) < limit {
		phase := legacyBananapulsePhases[phaseIndex]
		records, err := s.reader.ReadLegacyPhase(ctx, phase, after, limit-len(batch.Rows))
		if err != nil {
			return legacyImportBatch{}, fmt.Errorf("read %s phase: %w", phase, err)
		}
		for _, record := range records {
			identity, err := legacyBananapulseIdentity(phase, record.ID, record.Payload)
			if err != nil {
				return legacyImportBatch{}, fmt.Errorf("derive %s import identity: %w", phase, err)
			}
			batch.Rows = append(batch.Rows, legacyImportRow{
				Cursor:   encodeLegacyBananapulseCursor(phaseIndex, record.SortKey),
				SortKey:  fmt.Sprintf("%02d:%s", phaseIndex, record.SortKey),
				Entity:   phase,
				LegacyID: record.ID,
				Identity: identity,
				Payload:  append(json.RawMessage(nil), record.Payload...),
			})
		}
		if len(records) == limit-len(batch.Rows)+len(records) {
			break
		}
		phaseIndex++
		after = ""
	}
	batch.Done = phaseIndex >= len(legacyBananapulsePhases)
	return batch, nil
}

func encodeLegacyBananapulseCursor(phase int, sortKey string) string {
	return strconv.Itoa(phase) + ":" + base64.RawURLEncoding.EncodeToString([]byte(sortKey))
}

func decodeLegacyBananapulseCursor(cursor string) (int, string, error) {
	if cursor == "" {
		return 0, "", nil
	}
	parts := strings.SplitN(cursor, ":", 2)
	if len(parts) != 2 {
		return 0, "", errors.New("invalid legacy import cursor")
	}
	phase, err := strconv.Atoi(parts[0])
	if err != nil || phase < 0 || phase >= len(legacyBananapulsePhases) {
		return 0, "", errors.New("invalid legacy import cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, "", errors.New("invalid legacy import cursor")
	}
	return phase, string(raw), nil
}

func legacyBananapulseIdentity(entity, id string, payload json.RawMessage) ([]byte, error) {
	switch entity {
	case "source", "source_revoke":
		var row legacySourceRow
		if err := json.Unmarshal(payload, &row); err != nil {
			return nil, err
		}
		row.TokenHash = ""
		return json.Marshal(row)
	case "api_token":
		var row legacyAPITokenRow
		if err := json.Unmarshal(payload, &row); err != nil {
			return nil, err
		}
		row.TokenHash = ""
		return json.Marshal(row)
	case "subscriber":
		var row legacySubscriberRow
		if err := json.Unmarshal(payload, &row); err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			ID          string     `json:"id"`
			ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
			CreatedAt   time.Time  `json:"created_at"`
		}{ID: row.ID, ConfirmedAt: row.ConfirmedAt, CreatedAt: row.CreatedAt})
	default:
		return append([]byte(entity+"\x00"+id+"\x00"), payload...), nil
	}
}

type legacyPulpDestination struct {
	client sourceLifecycleClient
}

func newBananapulseLegacyImportService(
	legacyPostgres *sql.DB,
	checkpointSQLite *sql.DB,
	client sourceLifecycleClient,
	verifier legacyImportInvariantVerifier,
	batchSize int,
) (*legacyImportService, error) {
	return newBananapulseLegacyImportServiceFromQueryer(
		legacyPostgres, checkpointSQLite, client, verifier, batchSize,
	)
}

func newBananapulseLegacyImportServiceFromQueryer(
	legacyPostgres legacySQLQueryer,
	checkpointSQLite *sql.DB,
	client sourceLifecycleClient,
	verifier legacyImportInvariantVerifier,
	batchSize int,
) (*legacyImportService, error) {
	reader, err := newPostgresLegacyPhaseReader(legacyPostgres)
	if err != nil {
		return nil, err
	}
	source, err := newLegacyBananapulseRowSource(reader)
	if err != nil {
		return nil, err
	}
	destination, err := newLegacyPulpDestination(client)
	if err != nil {
		return nil, err
	}
	checkpoints, err := newSQLiteLegacyImportCheckpointStore(checkpointSQLite)
	if err != nil {
		return nil, err
	}
	return newLegacyImportService(source, destination, checkpoints, verifier, batchSize)
}

func newLegacyPulpDestination(client sourceLifecycleClient) (*legacyPulpDestination, error) {
	if client == nil {
		return nil, errors.New("Pulp destination client is required")
	}
	return &legacyPulpDestination{client: client}, nil
}

func (d *legacyPulpDestination) ApplyLegacyImport(
	ctx context.Context,
	envelope legacyImportEnvelope,
) (legacyImportReceipt, error) {
	payload, ok := envelope.Payload.(json.RawMessage)
	if !ok {
		return legacyImportReceipt{}, errors.New("legacy import payload is not canonical JSON")
	}
	if err := d.apply(ctx, envelope, payload); err != nil {
		return legacyImportReceipt{}, err
	}
	return legacyImportReceipt{ImportID: envelope.ImportID, Applied: true}, nil
}

func (d *legacyPulpDestination) apply(ctx context.Context, envelope legacyImportEnvelope, payload json.RawMessage) error {
	switch envelope.Entity {
	case "component":
		var row legacyComponentRow
		if err := json.Unmarshal(payload, &row); err != nil {
			return err
		}
		status := legacyFallbackStatus(row.Status)
		command := bridgeMonitorCommand{
			Version: "monitor.v1", ID: envelope.ImportID, Kind: "upsert_component",
			AtUnix: row.CreatedAt.Unix(), ImportMode: true,
			Component: &bridgeMonitorComponent{
				ID: row.ID, ParentID: row.ParentID, Name: row.Name, Kind: row.Kind,
				Tag: row.Tag, Brand: row.Brand, Domain: row.Domain, Uptime90D: row.Uptime90D,
				SortOrder: row.SortOrder, FallbackStatus: status, Launched: row.Launched,
				LaunchedSet: true, CreatedAtUnix: row.CreatedAt.Unix(),
				Archived: false,
			},
		}
		return d.client.callRaw(ctx, eventMonitorMigrationImport, command, &map[string]any{})
	case "source":
		var row legacySourceRow
		if err := json.Unmarshal(payload, &row); err != nil {
			return err
		}
		var authResult any
		if err := d.client.callProviderRaw(ctx, authOwnerCell, providerAuthSourceAdminImport, bridgeAuthSourceCredentialImportRequest{
			Version: "bananapulse.auth/v1", RequestID: envelope.ImportID + "/credential",
			CredentialID: row.ID, SourceID: row.ID, TokenDigest: row.TokenHash,
			CreatedAt: row.CreatedAt, RevokedAt: row.RevokedAt,
		}, &authResult); err != nil {
			return err
		}
		command := bridgeMonitorCommand{
			Version: "monitor.v1", ID: envelope.ImportID + "/monitor", Kind: "upsert_source",
			AtUnix: row.CreatedAt.Unix(), ImportMode: true,
			Source: &bridgeMonitorSource{
				ID: row.ID, Name: row.Name, Weight: row.Weight, Kind: row.Kind, Trusted: row.Trusted,
				DirectTargets: legacyMonitorSourceDirectTargets(row.Name, row.Kind, row.RevokedAt != nil),
				DefaultTTL:    row.DefaultTTL, CreatedAtUnix: row.CreatedAt.Unix(),
				Revoked: false,
			},
		}
		return d.client.callRaw(ctx, eventMonitorMigrationImport, command, &map[string]any{})
	case "mapping":
		var row legacyMappingRow
		if err := json.Unmarshal(payload, &row); err != nil {
			return err
		}
		return d.client.callRaw(ctx, eventMonitorMigrationImport, bridgeMonitorCommand{
			Version: "monitor.v1", ID: envelope.ImportID, Kind: "map_source_target", ImportMode: true,
			Mapping: &bridgeSourceTargetMapping{
				ID: row.ID, SourceID: row.SourceID, RawLabel: row.RawLabel, ComponentID: row.ComponentID,
			},
		}, &map[string]any{})
	case "observation":
		var row legacyObservationRow
		if err := json.Unmarshal(payload, &row); err != nil {
			return err
		}
		return d.client.callRaw(ctx, eventMonitorMigrationImport, bridgeMonitorCommand{
			Version: "monitor.v1", ID: envelope.ImportID, Kind: "append_observation",
			AtUnix: row.ObservedAt.Unix(), ImportMode: true,
			Observation: &bridgeObservation{
				ID: row.ID, SourceID: row.SourceID, ComponentID: row.ComponentID,
				Signal: row.Signal, Detail: row.Detail, ObservedAtUnix: row.ObservedAt.Unix(),
				ExpiresAtUnix: unixOrZero(row.ExpiresAt),
			},
		}, &map[string]any{})
	case "incident":
		var row legacyIncidentRow
		if err := json.Unmarshal(payload, &row); err != nil {
			return err
		}
		return d.client.callRaw(ctx, eventMonitorMigrationImport, bridgeMonitorCommand{
			Version: "monitor.v1", ID: envelope.ImportID, Kind: "open_incident",
			AtUnix: row.CreatedAt.Unix(), ImportMode: true,
			Incident: &bridgeMonitorIncident{
				ID: row.ID, Title: row.Title, Summary: row.Summary, Status: row.Status,
				Severity: row.Severity, Affects: row.Affects, Auto: row.Auto,
				StartedAtUnix: row.StartedAt.Unix(), ResolvedAtUnix: unixOrZero(row.ResolvedAt),
				CreatedAtUnix: row.CreatedAt.Unix(),
			},
		}, &map[string]any{})
	case "incident_update":
		var row legacyIncidentUpdateRow
		if err := json.Unmarshal(payload, &row); err != nil {
			return err
		}
		return d.client.callRaw(ctx, eventMonitorMigrationImport, bridgeMonitorCommand{
			Version: "monitor.v1", ID: envelope.ImportID, Kind: "update_incident",
			AtUnix: row.At.Unix(), ImportMode: true,
			Update: &bridgeIncidentUpdate{
				ID: row.ID, IncidentID: row.IncidentID, AtUnix: row.At.Unix(),
				Label: row.Label, Body: row.Body, Author: row.Author,
			},
		}, &map[string]any{})
	case "maintenance":
		var row legacyMaintenanceRow
		if err := json.Unmarshal(payload, &row); err != nil {
			return err
		}
		kind := row.Kind
		if kind == "" {
			kind = "scheduled"
		}
		return d.client.callRaw(ctx, eventMonitorMigrationImport, bridgeMonitorCommand{
			Version: "monitor.v1", ID: envelope.ImportID, Kind: "schedule_maintenance",
			AtUnix: row.CreatedAt.Unix(), ImportMode: true,
			Maintenance: &bridgeMaintenance{
				ID: row.ID, Title: row.Title, Summary: row.Summary, Kind: kind,
				ScheduledStartUnix: row.ScheduledStart.Unix(), ScheduledEndUnix: row.ScheduledEnd.Unix(),
				Affects: row.Affects, CreatedAtUnix: row.CreatedAt.Unix(),
			},
		}, &map[string]any{})
	case "component_archive":
		var row legacyComponentRow
		if err := json.Unmarshal(payload, &row); err != nil {
			return err
		}
		return d.client.callRaw(ctx, eventMonitorMigrationImport, bridgeMonitorCommand{
			Version: "monitor.v1", ID: envelope.ImportID, Kind: "archive_component",
			AtUnix: unixOrZero(row.ArchivedAt), ImportMode: true, ComponentID: row.ID,
			ArchiveBatchID: "legacy-import",
		}, &map[string]any{})
	case "source_revoke":
		var row legacySourceRow
		if err := json.Unmarshal(payload, &row); err != nil {
			return err
		}
		return d.client.callRaw(ctx, eventMonitorMigrationImport, bridgeMonitorCommand{
			Version: "monitor.v1", ID: envelope.ImportID, Kind: "revoke_source",
			AtUnix: unixOrZero(row.RevokedAt), ImportMode: true, SourceID: row.ID,
		}, &map[string]any{})
	case "subscriber":
		var row legacySubscriberRow
		if err := json.Unmarshal(payload, &row); err != nil {
			return err
		}
		return d.client.callRaw(ctx, eventSubscriberMigrationImport, bridgeSubscriberMigrationImportRequest{
			Version: subscriberContractVersion, RequestID: envelope.ImportID,
			Subscribers: []bridgeLegacySubscriberRow{{
				ID: row.ID, Email: row.Email, ConfirmedAt: row.ConfirmedAt, CreatedAt: row.CreatedAt,
			}},
		}, &map[string]any{})
	case "api_token":
		var row legacyAPITokenRow
		if err := json.Unmarshal(payload, &row); err != nil {
			return err
		}
		var result any
		return d.client.callProviderRaw(ctx, authOwnerCell, providerAuthAPITokenAdminImport, bridgeAuthAPITokenImportRequest{
			Version: "bananapulse.auth/v1", RequestID: envelope.ImportID,
			TokenID: row.ID, Name: row.Name, Scope: row.Scope, TokenDigest: row.TokenHash,
			CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt, RevokedAt: row.RevokedAt, LastUsedAt: row.LastUsedAt,
		}, &result)
	default:
		return fmt.Errorf("unsupported legacy import entity %q", envelope.Entity)
	}
}

func legacyFallbackStatus(value string) string {
	switch value {
	case "ok", "operational", "":
		return "operational"
	case "degraded":
		return "degraded"
	default:
		return "outage"
	}
}

func unixOrZero(value *time.Time) int64 {
	if value == nil {
		return 0
	}
	return value.Unix()
}

type legacyComponentRow struct {
	ID         string            `json:"id"`
	ParentID   string            `json:"parent_id"`
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	Tag        string            `json:"tag"`
	Status     string            `json:"status"`
	Uptime90D  []bridgeUptimeDay `json:"uptime_90d"`
	SortOrder  int               `json:"sort_order"`
	Brand      string            `json:"brand"`
	Domain     string            `json:"domain"`
	Launched   bool              `json:"launched"`
	CreatedAt  time.Time         `json:"created_at"`
	ArchivedAt *time.Time        `json:"archived_at"`
}

type legacySourceRow struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	TokenHash  string     `json:"token_hash"`
	Weight     int        `json:"weight"`
	Kind       string     `json:"kind"`
	Trusted    bool       `json:"trusted"`
	DefaultTTL *int64     `json:"default_ttl"`
	CreatedAt  time.Time  `json:"created_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

type legacyMappingRow struct {
	ID          string `json:"id"`
	SourceID    string `json:"source_id"`
	RawLabel    string `json:"raw_label"`
	ComponentID string `json:"component_id"`
}

type legacyObservationRow struct {
	ID          string     `json:"id"`
	SourceID    string     `json:"source_id"`
	ComponentID string     `json:"component_id"`
	Signal      string     `json:"signal"`
	Detail      string     `json:"detail"`
	ObservedAt  time.Time  `json:"observed_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

type legacyIncidentRow struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Summary    string     `json:"summary"`
	Status     string     `json:"status"`
	Severity   string     `json:"severity"`
	Affects    []string   `json:"affects"`
	Auto       bool       `json:"auto"`
	StartedAt  time.Time  `json:"started_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

type legacyIncidentUpdateRow struct {
	ID         string    `json:"id"`
	IncidentID string    `json:"incident_id"`
	At         time.Time `json:"at"`
	Label      string    `json:"label"`
	Body       string    `json:"body"`
	Author     string    `json:"author"`
}

type legacyMaintenanceRow struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Summary        string    `json:"summary"`
	Kind           string    `json:"kind"`
	ScheduledStart time.Time `json:"scheduled_start"`
	ScheduledEnd   time.Time `json:"scheduled_end"`
	Affects        []string  `json:"affects"`
	CreatedAt      time.Time `json:"created_at"`
}

type legacySubscriberRow struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	ConfirmedAt *time.Time `json:"confirmed_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

type legacyAPITokenRow struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	TokenHash  string     `json:"token_hash"`
	Scope      string     `json:"scope"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

func nonSecretLegacyID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
