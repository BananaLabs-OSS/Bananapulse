package main

import (
	"fmt"

	"github.com/vmihailenco/msgpack/v5"
)

const ContractVersion = "monitor.v1"

const (
	FnCatalog    = "monitor.v1.catalog"
	FnCommand    = "monitor.v1.command"
	FnQuery      = "monitor.v1.query"
	FnProjection = "monitor.v1.projection"
)

type Signal string

const (
	SignalOK       Signal = "ok"
	SignalDegraded Signal = "degraded"
	SignalDown     Signal = "down"
)

type Status string

const (
	StatusOperational Status = "operational"
	StatusDegraded    Status = "degraded"
	StatusOutage      Status = "outage"
)

type SourceKind string

const (
	SourcePush      SourceKind = "push"
	SourceProbe     SourceKind = "probe"
	SourceHeartbeat SourceKind = "heartbeat"
	SourceManual    SourceKind = "manual"
)

type Component struct {
	ID             string      `msgpack:"id"`
	ParentID       string      `msgpack:"parent_id,omitempty"`
	Name           string      `msgpack:"name"`
	Kind           string      `msgpack:"kind"`
	Tag            string      `msgpack:"tag,omitempty"`
	Brand          string      `msgpack:"brand,omitempty"`
	Domain         string      `msgpack:"domain,omitempty"`
	Uptime90D      []UptimeDay `msgpack:"uptime_90d,omitempty"`
	SortOrder      int         `msgpack:"sort_order,omitempty"`
	FallbackStatus Status      `msgpack:"fallback_status"`
	Critical       bool        `msgpack:"critical"`
	Launched       bool        `msgpack:"launched"`
	LaunchedSet    bool        `msgpack:"launched_set,omitempty"`
	CreatedAtUnix  int64       `msgpack:"created_at_unix,omitempty"`
	Archived       bool        `msgpack:"archived"`
	ArchivedAtUnix int64       `msgpack:"archived_at_unix,omitempty"`
	ArchiveBatchID string      `msgpack:"archive_batch_id,omitempty"`
}

type UptimeDay struct {
	Date         string
	Status       string
	LegacyStatus string
}

func (value UptimeDay) MarshalMsgpack() ([]byte, error) {
	if value.Date == "" && value.LegacyStatus != "" {
		return msgpack.Marshal(value.LegacyStatus)
	}
	return msgpack.Marshal(struct {
		Date   string `msgpack:"date"`
		Status string `msgpack:"status"`
	}{Date: value.Date, Status: value.Status})
}

func (value *UptimeDay) UnmarshalMsgpack(raw []byte) error {
	var legacy string
	if err := msgpack.Unmarshal(raw, &legacy); err == nil {
		value.Date = ""
		value.Status = ""
		value.LegacyStatus = legacy
		return nil
	}
	var day struct {
		Date   string `msgpack:"date"`
		Status string `msgpack:"status"`
	}
	if err := msgpack.Unmarshal(raw, &day); err != nil {
		return fmt.Errorf("decode uptime day: %w", err)
	}
	value.Date = day.Date
	value.Status = day.Status
	value.LegacyStatus = ""
	return nil
}

type Source struct {
	ID            string     `msgpack:"id"`
	Name          string     `msgpack:"name"`
	Weight        int        `msgpack:"weight"`
	Kind          SourceKind `msgpack:"kind"`
	Trusted       bool       `msgpack:"trusted"`
	DirectTargets bool       `msgpack:"direct_targets"`
	DefaultTTL    *int64     `msgpack:"default_ttl_seconds"`
	CreatedAtUnix int64      `msgpack:"created_at_unix,omitempty"`
	Revoked       bool       `msgpack:"revoked"`
	RevokedAtUnix int64      `msgpack:"revoked_at_unix,omitempty"`
}

type SourceTargetMapping struct {
	ID          string `msgpack:"id"`
	SourceID    string `msgpack:"source_id"`
	RawLabel    string `msgpack:"raw_label"`
	ComponentID string `msgpack:"component_id"`
}

// Observation is immutable once accepted. ExpiresAtUnix is zero only for an
// intentionally non-expiring read (normally a manual non-OK declaration).
type Observation struct {
	ID             string `msgpack:"id"`
	SourceID       string `msgpack:"source_id"`
	ComponentID    string `msgpack:"component_id"`
	Signal         Signal `msgpack:"signal"`
	Detail         string `msgpack:"detail,omitempty"`
	ObservedAtUnix int64  `msgpack:"observed_at_unix"`
	ExpiresAtUnix  int64  `msgpack:"expires_at_unix,omitempty"`
}

type Incident struct {
	ID             string   `msgpack:"id"`
	Title          string   `msgpack:"title"`
	Summary        string   `msgpack:"summary"`
	Status         string   `msgpack:"status"`
	Severity       string   `msgpack:"severity"`
	Affects        []string `msgpack:"affects"`
	Auto           bool     `msgpack:"auto"`
	StartedAtUnix  int64    `msgpack:"started_at_unix"`
	ResolvedAtUnix int64    `msgpack:"resolved_at_unix,omitempty"`
	CreatedAtUnix  int64    `msgpack:"created_at_unix,omitempty"`
}

type IncidentUpdate struct {
	ID         string `msgpack:"id"`
	IncidentID string `msgpack:"incident_id"`
	AtUnix     int64  `msgpack:"at_unix"`
	Label      string `msgpack:"label"`
	Body       string `msgpack:"body"`
	Author     string `msgpack:"author"`
}

type Maintenance struct {
	ID                 string   `msgpack:"id"`
	Title              string   `msgpack:"title"`
	Summary            string   `msgpack:"summary"`
	Kind               string   `msgpack:"kind"`
	ScheduledStartUnix int64    `msgpack:"scheduled_start_unix"`
	ScheduledEndUnix   int64    `msgpack:"scheduled_end_unix"`
	Affects            []string `msgpack:"affects"`
	CreatedAtUnix      int64    `msgpack:"created_at_unix,omitempty"`
	Cancelled          bool     `msgpack:"cancelled"`
	CancelledAtUnix    int64    `msgpack:"cancelled_at_unix,omitempty"`
}

type CommandKind string

const (
	UpsertComponent     CommandKind = "upsert_component"
	EditComponent       CommandKind = "edit_component"
	ArchiveComponent    CommandKind = "archive_component"
	RestoreComponent    CommandKind = "restore_component"
	UpsertSource        CommandKind = "upsert_source"
	EditSource          CommandKind = "edit_source"
	RevokeSource        CommandKind = "revoke_source"
	RestoreSource       CommandKind = "restore_source"
	MapSourceTarget     CommandKind = "map_source_target"
	UnmapSourceTarget   CommandKind = "unmap_source_target"
	AppendObservation   CommandKind = "append_observation"
	IngestObservation   CommandKind = "ingest_observation"
	OpenIncident        CommandKind = "open_incident"
	EditIncident        CommandKind = "edit_incident"
	UpdateIncident      CommandKind = "update_incident"
	ResolveIncident     CommandKind = "resolve_incident"
	DeleteIncident      CommandKind = "delete_incident"
	ScheduleMaintenance CommandKind = "schedule_maintenance"
	EditMaintenance     CommandKind = "edit_maintenance"
	CancelMaintenance   CommandKind = "cancel_maintenance"
	DeleteMaintenance   CommandKind = "delete_maintenance"
	SweepReconcile      CommandKind = "sweep_reconcile"
)

type ComponentPatch struct {
	ID             string       `msgpack:"id"`
	ParentID       *string      `msgpack:"parent_id,omitempty"`
	Name           *string      `msgpack:"name,omitempty"`
	Kind           *string      `msgpack:"kind,omitempty"`
	Tag            *string      `msgpack:"tag,omitempty"`
	Brand          *string      `msgpack:"brand,omitempty"`
	Domain         *string      `msgpack:"domain,omitempty"`
	Uptime90D      *[]UptimeDay `msgpack:"uptime_90d,omitempty"`
	SortOrder      *int         `msgpack:"sort_order,omitempty"`
	FallbackStatus *Status      `msgpack:"fallback_status,omitempty"`
	Critical       *bool        `msgpack:"critical,omitempty"`
	Launched       *bool        `msgpack:"launched,omitempty"`
}

type SourcePatch struct {
	ID            string      `msgpack:"id"`
	Name          *string     `msgpack:"name,omitempty"`
	Weight        *int        `msgpack:"weight,omitempty"`
	Kind          *SourceKind `msgpack:"kind,omitempty"`
	Trusted       *bool       `msgpack:"trusted,omitempty"`
	DirectTargets *bool       `msgpack:"direct_targets,omitempty"`
	DefaultTTL    *int64      `msgpack:"default_ttl_seconds,omitempty"`
	DefaultTTLSet bool        `msgpack:"default_ttl_seconds_set,omitempty"`
}

type IncidentPatch struct {
	ID       string   `msgpack:"id"`
	Title    *string  `msgpack:"title,omitempty"`
	Summary  *string  `msgpack:"summary,omitempty"`
	Status   *string  `msgpack:"status,omitempty"`
	Severity *string  `msgpack:"severity,omitempty"`
	Affects  []string `msgpack:"affects,omitempty"`
	AtUnix   int64    `msgpack:"at_unix,omitempty"`
	Author   string   `msgpack:"author,omitempty"`
	Note     string   `msgpack:"note,omitempty"`
}

type MaintenancePatch struct {
	ID                 string   `msgpack:"id"`
	Title              *string  `msgpack:"title,omitempty"`
	Summary            *string  `msgpack:"summary,omitempty"`
	Kind               *string  `msgpack:"kind,omitempty"`
	ScheduledStartUnix *int64   `msgpack:"scheduled_start_unix,omitempty"`
	ScheduledEndUnix   *int64   `msgpack:"scheduled_end_unix,omitempty"`
	Affects            []string `msgpack:"affects,omitempty"`
}

type IngestRequest struct {
	ObservationID  string `msgpack:"observation_id"`
	SourceID       string `msgpack:"source_id"`
	RawLabel       string `msgpack:"raw_label,omitempty"`
	ComponentID    string `msgpack:"component_id,omitempty"`
	Signal         Signal `msgpack:"signal"`
	Detail         string `msgpack:"detail,omitempty"`
	ObservedAtUnix int64  `msgpack:"observed_at_unix,omitempty"`
	ExpiresAtUnix  int64  `msgpack:"expires_at_unix,omitempty"`
}

type Command struct {
	Version          string               `msgpack:"version"`
	ID               string               `msgpack:"id"`
	Kind             CommandKind          `msgpack:"kind"`
	AtUnix           int64                `msgpack:"at_unix"`
	ImportMode       bool                 `msgpack:"import_mode,omitempty"`
	Component        *Component           `msgpack:"component,omitempty"`
	ComponentPatch   *ComponentPatch      `msgpack:"component_patch,omitempty"`
	ComponentID      string               `msgpack:"component_id,omitempty"`
	ArchiveBatchID   string               `msgpack:"archive_batch_id,omitempty"`
	Source           *Source              `msgpack:"source,omitempty"`
	SourcePatch      *SourcePatch         `msgpack:"source_patch,omitempty"`
	SourceID         string               `msgpack:"source_id,omitempty"`
	Mapping          *SourceTargetMapping `msgpack:"mapping,omitempty"`
	MappingID        string               `msgpack:"mapping_id,omitempty"`
	Observation      *Observation         `msgpack:"observation,omitempty"`
	Ingest           *IngestRequest       `msgpack:"ingest,omitempty"`
	Incident         *Incident            `msgpack:"incident,omitempty"`
	IncidentID       string               `msgpack:"incident_id,omitempty"`
	IncidentPatch    *IncidentPatch       `msgpack:"incident_patch,omitempty"`
	Update           *IncidentUpdate      `msgpack:"update,omitempty"`
	Maintenance      *Maintenance         `msgpack:"maintenance,omitempty"`
	MaintenancePatch *MaintenancePatch    `msgpack:"maintenance_patch,omitempty"`
	MaintenanceID    string               `msgpack:"maintenance_id,omitempty"`
}

type CommandResult struct {
	Version      string             `msgpack:"version"`
	CommandID    string             `msgpack:"command_id"`
	Revision     uint64             `msgpack:"revision"`
	Deduped      bool               `msgpack:"deduped"`
	ComponentIDs []string           `msgpack:"component_ids,omitempty"`
	MappingID    string             `msgpack:"mapping_id,omitempty"`
	Evaluation   *IngestEvaluation  `msgpack:"evaluation,omitempty"`
	Sweep        *SweepResult       `msgpack:"sweep,omitempty"`
	Transitions  []DomainTransition `msgpack:"transitions,omitempty"`
}

type DomainTransition struct {
	ID                   string          `msgpack:"id"`
	Kind                 string          `msgpack:"kind"`
	EntityID             string          `msgpack:"entity_id"`
	ComponentID          string          `msgpack:"component_id,omitempty"`
	AffectedComponentIDs []string        `msgpack:"affected_component_ids,omitempty"`
	Status               string          `msgpack:"status,omitempty"`
	PreviousStatus       string          `msgpack:"previous_status,omitempty"`
	Severity             string          `msgpack:"severity,omitempty"`
	PreviousSeverity     string          `msgpack:"previous_severity,omitempty"`
	AtUnix               int64           `msgpack:"at_unix,omitempty"`
	Incident             *Incident       `msgpack:"incident,omitempty"`
	IncidentUpdate       *IncidentUpdate `msgpack:"incident_update,omitempty"`
	Maintenance          *Maintenance    `msgpack:"maintenance,omitempty"`
}

type IngestEvaluation struct {
	ObservationID   string `msgpack:"observation_id"`
	ComponentID     string `msgpack:"component_id"`
	State           string `msgpack:"state"`
	Level           string `msgpack:"level,omitempty"`
	NonOK           int    `msgpack:"non_ok"`
	Sources         int    `msgpack:"sources"`
	ReducedCoverage bool   `msgpack:"reduced_coverage"`
}

type ReconcileTransition struct {
	ComponentID      string `msgpack:"component_id"`
	IncidentID       string `msgpack:"incident_id,omitempty"`
	PreviousState    string `msgpack:"previous_state,omitempty"`
	State            string `msgpack:"state"`
	PreviousSeverity string `msgpack:"previous_severity,omitempty"`
	Severity         string `msgpack:"severity,omitempty"`
}

type SweepResult struct {
	AtUnix          int64                 `msgpack:"at_unix"`
	Components      int                   `msgpack:"components"`
	Declared        int                   `msgpack:"declared"`
	Watch           int                   `msgpack:"watch"`
	ReducedCoverage int                   `msgpack:"reduced_coverage"`
	Transitions     []ReconcileTransition `msgpack:"transitions"`
}

type SourceRead struct {
	SourceID       string     `msgpack:"source_id"`
	SourceName     string     `msgpack:"source_name"`
	Weight         int        `msgpack:"weight"`
	Trusted        bool       `msgpack:"trusted"`
	Kind           SourceKind `msgpack:"kind"`
	Signal         Signal     `msgpack:"signal"`
	ObservedAtUnix int64      `msgpack:"observed_at_unix"`
	ExpiresAtUnix  int64      `msgpack:"expires_at_unix,omitempty"`
	Stale          bool       `msgpack:"stale"`
}

type ComponentEvaluation struct {
	ComponentID       string       `msgpack:"component_id"`
	Status            Status       `msgpack:"status"`
	State             string       `msgpack:"state"`
	Level             string       `msgpack:"level,omitempty"`
	Reads             []SourceRead `msgpack:"reads"`
	NonOKCount        int          `msgpack:"non_ok_count"`
	NonOKWeight       int          `msgpack:"non_ok_weight"`
	TrustedNonOKCount int          `msgpack:"trusted_non_ok_count"`
	StaleCount        int          `msgpack:"stale_count"`
	ReducedCoverage   bool         `msgpack:"reduced_coverage"`
	HasLiveReads      bool         `msgpack:"has_live_reads"`
}

type ComponentProjection struct {
	Component     Component           `msgpack:"component"`
	OwnEvaluation ComponentEvaluation `msgpack:"own_evaluation"`
	Evaluation    ComponentEvaluation `msgpack:"evaluation"`
}

type Projection struct {
	Version         string                `msgpack:"version"`
	Revision        uint64                `msgpack:"revision"`
	Components      []ComponentProjection `msgpack:"components"`
	Sources         []Source              `msgpack:"sources"`
	Mappings        []SourceTargetMapping `msgpack:"mappings"`
	Observations    []Observation         `msgpack:"observations,omitempty"`
	Incidents       []Incident            `msgpack:"incidents"`
	IncidentUpdates []IncidentUpdate      `msgpack:"incident_updates"`
	Maintenance     []Maintenance         `msgpack:"maintenance"`
}

type Query struct {
	Version             string `msgpack:"version"`
	ComponentID         string `msgpack:"component_id,omitempty"`
	SourceID            string `msgpack:"source_id,omitempty"`
	IncidentID          string `msgpack:"incident_id,omitempty"`
	MaintenanceID       string `msgpack:"maintenance_id,omitempty"`
	IncludeArchived     bool   `msgpack:"include_archived,omitempty"`
	IncludeObservations bool   `msgpack:"include_observations,omitempty"`
	AtUnix              int64  `msgpack:"at_unix"`
}

type Catalog struct {
	Version  string   `msgpack:"version"`
	Commands []string `msgpack:"commands"`
	Queries  []string `msgpack:"queries"`
}

func validVersion(version string) error {
	if version != ContractVersion {
		return fmt.Errorf("unsupported monitor contract version %q", version)
	}
	return nil
}

func requireID(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	return nil
}

func validSignal(signal Signal) bool {
	return signal == SignalOK || signal == SignalDegraded || signal == SignalDown
}
