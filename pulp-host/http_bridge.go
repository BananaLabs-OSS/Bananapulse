package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

const (
	bridgeEventsPath  = "/internal/v1/events/"
	bridgeTokenHeader = "X-Pulp-Bridge-Token"
	maxBridgeBody     = 1 << 20
)

// httpBridge is deliberately an adapter, not an owner. It accepts only the
// explicit application events below, converts their JSON contracts to the
// owners' MessagePack contracts, and returns their domain results as JSON.
// It holds no business state and never calls a cell directly.
type httpBridge struct {
	client   *applicationClient
	token    string
	families bridgeFamilies
	sources  *sourceLifecycleService
}

func newHTTPBridge(client *applicationClient, token string) (*httpBridge, error) {
	return newHTTPBridgeWithFamilies(client, token, bridgeFamilies{})
}

func newHTTPBridgeWithFamilies(client *applicationClient, token string, families bridgeFamilies) (*httpBridge, error) {
	if client == nil {
		return nil, errors.New("application client is required")
	}
	return &httpBridge{client: client, token: token, families: families}, nil
}

func (b *httpBridge) Handler() http.Handler { return http.HandlerFunc(b.serveHTTP) }

func (b *httpBridge) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		writeBridgeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if !strings.HasPrefix(r.URL.Path, bridgeEventsPath) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !b.authorized(r) {
		writeBridgeError(w, http.StatusUnauthorized, "bridge authorization required")
		return
	}
	event := strings.TrimPrefix(r.URL.Path, bridgeEventsPath)
	if event == "" || strings.Contains(event, "/") {
		writeBridgeError(w, http.StatusNotFound, "unknown application event")
		return
	}
	if err := b.dispatchHTTP(r.Context(), event, r, w); err != nil {
		var requestErr *bridgeRequestError
		if errors.As(err, &requestErr) {
			writeBridgeError(w, http.StatusBadRequest, requestErr.Error())
			return
		}
		var directDomainErr *bridgeDomainError
		if errors.As(err, &directDomainErr) {
			writeBridgeJSON(w, directDomainErr.Status, map[string]string{
				"error": directDomainErr.Message,
				"code":  directDomainErr.Code,
			})
			return
		}
		if domainErr, ok := classifyBridgeDomainError(err); ok {
			writeBridgeJSON(w, domainErr.Status, map[string]string{
				"error": domainErr.Message,
				"code":  domainErr.Code,
			})
			return
		}
		writeBridgeError(w, http.StatusBadGateway, "application dispatch failed")
	}
}

func (b *httpBridge) authorized(r *http.Request) bool {
	if b.token == "" {
		return true
	}
	provided := r.Header.Get(bridgeTokenHeader)
	return subtle.ConstantTimeCompare([]byte(provided), []byte(b.token)) == 1
}

func (b *httpBridge) dispatchHTTP(ctx context.Context, event string, r *http.Request, w http.ResponseWriter) error {
	if event == eventHostSourceAdminCreate || event == eventHostSourceAdminRotate || event == eventHostSourceAdminRevoke {
		if !b.families.sourceAdmin || b.sources == nil {
			return disabledBridgeFamily()
		}
		switch event {
		case eventHostSourceAdminCreate:
			var request sourceAdminCreateRequest
			if err := decodeBridgeJSON(r, &request, false); err != nil {
				return err
			}
			result, err := b.sources.Create(ctx, request)
			if err != nil {
				return err
			}
			writeBridgeJSON(w, http.StatusOK, result)
			return nil
		case eventHostSourceAdminRotate:
			var request bridgeAuthSourceCredentialRotateRequest
			if err := decodeBridgeJSON(r, &request, false); err != nil {
				return err
			}
			result, err := b.sources.Rotate(ctx, request)
			if err != nil {
				return err
			}
			writeBridgeJSON(w, http.StatusOK, result)
			return nil
		case eventHostSourceAdminRevoke:
			var request sourceAdminRevokeRequest
			if err := decodeBridgeJSON(r, &request, false); err != nil {
				return err
			}
			result, err := b.sources.Revoke(ctx, request)
			if err != nil {
				return err
			}
			writeBridgeJSON(w, http.StatusOK, result)
			return nil
		}
	}
	if provider, request, ok := authBridgeRequest(event); ok {
		if !b.families.auth || (authAdminEvent(event) && !b.families.authAdmin) {
			return disabledBridgeFamily()
		}
		if err := decodeBridgeJSON(r, request, false); err != nil {
			return err
		}
		var response any
		if err := b.client.callProviderRaw(ctx, authOwnerCell, provider, request, &response); err != nil {
			return &bridgeDispatchError{event: event, err: err}
		}
		writeBridgeJSON(w, http.StatusOK, response)
		return nil
	}
	switch event {
	case eventMonitorCommand,
		eventMonitorAdminCommand,
		eventMonitorMigrationImport,
		eventMonitorIngestAuthenticated,
		eventMonitorSweep:
		var request bridgeMonitorCommand
		if err := decodeBridgeJSON(r, &request, false); err != nil {
			return err
		}
		if !b.monitorEventAllowed(event, request.Kind) {
			return disabledBridgeFamily()
		}
		return b.callAndWrite(ctx, event, request, w)
	case eventMonitorQuery:
		var request bridgeMonitorQuery
		if err := decodeBridgeJSON(r, &request, false); err != nil {
			return err
		}
		return b.callAndWrite(ctx, event, request, w)
	case eventMonitorProjection, eventSubscriberProjection:
		if err := decodeBridgeJSON(r, &struct{}{}, true); err != nil {
			return err
		}
		return b.callAndWrite(ctx, event, nil, w)
	case eventSubscriberSubscribe:
		var request bridgeSubscribeRequest
		if err := decodeBridgeJSON(r, &request, false); err != nil {
			return err
		}
		return b.callAndWrite(ctx, event, request, w)
	case eventSubscriberConfirm:
		var request bridgeConfirmRequest
		if err := decodeBridgeJSON(r, &request, false); err != nil {
			return err
		}
		return b.callAndWrite(ctx, event, request, w)
	case eventSubscriberUnsubscribe:
		var request bridgeUnsubscribeRequest
		if err := decodeBridgeJSON(r, &request, false); err != nil {
			return err
		}
		return b.callAndWrite(ctx, event, request, w)
	case eventSubscriberConfirmationResend:
		var request bridgeConfirmationResendRequest
		if err := decodeBridgeJSON(r, &request, false); err != nil {
			return err
		}
		return b.callAndWrite(ctx, event, request, w)
	case eventSubscriberAdminList:
		if !b.families.subscriberAdmin {
			return disabledBridgeFamily()
		}
		var request bridgeSubscriberAdminListRequest
		if err := decodeBridgeJSON(r, &request, false); err != nil {
			return err
		}
		return b.callAndWrite(ctx, event, request, w)
	case eventSubscriberAdminGet:
		if !b.families.subscriberAdmin {
			return disabledBridgeFamily()
		}
		var request bridgeSubscriberAdminGetRequest
		if err := decodeBridgeJSON(r, &request, false); err != nil {
			return err
		}
		return b.callAndWrite(ctx, event, request, w)
	case eventSubscriberAdminDelete:
		if !b.families.subscriberAdmin {
			return disabledBridgeFamily()
		}
		var request bridgeSubscriberAdminDeleteRequest
		if err := decodeBridgeJSON(r, &request, false); err != nil {
			return err
		}
		return b.callAndWrite(ctx, event, request, w)
	case eventSubscriberAdminStateSet:
		if !b.families.subscriberAdmin {
			return disabledBridgeFamily()
		}
		var request bridgeSubscriberAdminStateSetRequest
		if err := decodeBridgeJSON(r, &request, false); err != nil {
			return err
		}
		return b.callAndWrite(ctx, event, request, w)
	case eventSubscriberMigrationImport:
		if !b.families.migration {
			return disabledBridgeFamily()
		}
		var request bridgeSubscriberMigrationImportRequest
		if err := decodeBridgeJSON(r, &request, false); err != nil {
			return err
		}
		return b.callAndWrite(ctx, event, request, w)
	case eventIncidentPublish, eventMaintenancePublish:
		if !b.families.monitorAdmin {
			return disabledBridgeFamily()
		}
		var request bridgePublishRequest
		if err := decodeBridgeJSON(r, &request, false); err != nil {
			return err
		}
		var monitorResult any
		var notificationResult any
		if err := b.client.publish(ctx, event, request.MonitorRequest, request.NotificationRequest, &monitorResult, &notificationResult); err != nil {
			return &bridgeDispatchError{event: event, err: err}
		}
		writeBridgeJSON(w, http.StatusOK, bridgePublishResponse{Monitor: monitorResult, Notification: notificationResult})
		return nil
	case eventEmailOutboxClaim:
		var request outboxClaimRequest
		if err := decodeBridgeJSON(r, &request, false); err != nil {
			return err
		}
		return b.callAndWrite(ctx, event, request, w)
	case eventEmailReceiptApply:
		var request outboxReceipt
		if err := decodeBridgeJSON(r, &request, false); err != nil {
			return err
		}
		return b.callAndWrite(ctx, event, request, w)
	default:
		return &bridgeRequestError{message: "unknown application event"}
	}
}

func (b *httpBridge) monitorEventAllowed(event, kind string) bool {
	switch event {
	case eventMonitorAdminCommand:
		return b.families.monitorAdmin && kind != "ingest_observation" && kind != "sweep_reconcile"
	case eventMonitorMigrationImport:
		return b.families.migration && kind != "ingest_observation" && kind != "sweep_reconcile"
	case eventMonitorIngestAuthenticated:
		return b.families.monitorIngest && kind == "ingest_observation"
	case eventMonitorSweep:
		return b.families.monitorSweep && kind == "sweep_reconcile"
	case eventMonitorCommand:
		if kind == "ingest_observation" {
			return b.families.monitorIngest
		}
		if kind == "sweep_reconcile" {
			return b.families.monitorSweep
		}
		return !monitorCommandRequiresAdmin(kind) || b.families.monitorAdmin
	default:
		return false
	}
}

type bridgeFamilies struct {
	monitorAdmin    bool
	monitorIngest   bool
	monitorSweep    bool
	subscriberAdmin bool
	migration       bool
	auth            bool
	authAdmin       bool
	sourceAdmin     bool
}

func disabledBridgeFamily() error {
	return &bridgeRequestError{message: "application event family is disabled"}
}

func monitorCommandRequiresAdmin(kind string) bool {
	switch kind {
	case "upsert_component",
		"edit_component",
		"archive_component",
		"restore_component",
		"upsert_source",
		"edit_source",
		"revoke_source",
		"restore_source",
		"map_source_target",
		"unmap_source_target",
		"open_incident",
		"edit_incident",
		"update_incident",
		"resolve_incident",
		"delete_incident",
		"schedule_maintenance",
		"edit_maintenance",
		"cancel_maintenance",
		"delete_maintenance":
		return true
	default:
		return false
	}
}

func (b *httpBridge) callAndWrite(ctx context.Context, event string, request any, w http.ResponseWriter) error {
	var response any
	if err := b.client.callRaw(ctx, event, request, &response); err != nil {
		return &bridgeDispatchError{event: event, err: err}
	}
	writeBridgeJSON(w, http.StatusOK, response)
	return nil
}

type bridgeRequestError struct{ message string }

func (e *bridgeRequestError) Error() string { return e.message }

type bridgeDispatchError struct {
	event string
	err   error
}

func (e *bridgeDispatchError) Error() string { return e.err.Error() }
func (e *bridgeDispatchError) Unwrap() error { return e.err }

type bridgeDomainError struct {
	Status  int
	Code    string
	Message string
}

func (e *bridgeDomainError) Error() string { return e.Code }

func classifyBridgeDomainError(err error) (bridgeDomainError, bool) {
	var dispatchErr *bridgeDispatchError
	if !errors.As(err, &dispatchErr) || !monitorBridgeEvent(dispatchErr.event) {
		return bridgeDomainError{}, false
	}
	message := strings.ToLower(dispatchErr.err.Error())
	switch {
	case strings.Contains(message, "is not mapped"):
		return bridgeDomainError{
			Status: http.StatusUnprocessableEntity, Code: "unmapped_target", Message: "source target is not mapped",
		}, true
	case strings.Contains(message, "not found"):
		return bridgeDomainError{
			Status: http.StatusNotFound, Code: "not_found", Message: "requested monitor entity was not found",
		}, true
	case strings.Contains(message, "cannot archive") ||
		strings.Contains(message, "already exists") ||
		strings.Contains(message, " is archived") ||
		strings.Contains(message, " is revoked") ||
		strings.Contains(message, " is resolved") ||
		strings.Contains(message, " is cancelled"):
		return bridgeDomainError{
			Status: http.StatusConflict, Code: "conflict", Message: "monitor state conflicts with request",
		}, true
	case strings.Contains(message, "invalid ") ||
		strings.Contains(message, " is required") ||
		strings.Contains(message, " cannot ") ||
		strings.Contains(message, "cycle") ||
		strings.Contains(message, "expiry") ||
		strings.Contains(message, "time is required") ||
		strings.Contains(message, "schedule"):
		return bridgeDomainError{
			Status: http.StatusBadRequest, Code: "invalid_request", Message: "invalid monitor request",
		}, true
	default:
		return bridgeDomainError{}, false
	}
}

func monitorBridgeEvent(event string) bool {
	switch event {
	case eventMonitorCommand,
		eventMonitorAdminCommand,
		eventMonitorMigrationImport,
		eventMonitorIngestAuthenticated,
		eventMonitorSweep,
		eventMonitorQuery,
		eventMonitorProjection,
		eventIncidentPublish,
		eventMaintenancePublish:
		return true
	default:
		return false
	}
}

func decodeBridgeJSON(r *http.Request, target any, optional bool) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBridgeBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(target)
	if errors.Is(err, io.EOF) && optional {
		return nil
	}
	if err != nil {
		return &bridgeRequestError{message: "invalid JSON request"}
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return &bridgeRequestError{message: "request must contain one JSON value"}
	}
	return nil
}

func methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	writeBridgeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func writeBridgeError(w http.ResponseWriter, status int, message string) {
	writeBridgeJSON(w, status, map[string]string{"error": message})
}

func writeBridgeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type bridgeMonitorComponent struct {
	ID             string            `msgpack:"id" json:"id"`
	ParentID       string            `msgpack:"parent_id,omitempty" json:"parent_id,omitempty"`
	Name           string            `msgpack:"name" json:"name"`
	Kind           string            `msgpack:"kind" json:"kind"`
	Tag            string            `msgpack:"tag,omitempty" json:"tag,omitempty"`
	Brand          string            `msgpack:"brand,omitempty" json:"brand,omitempty"`
	Domain         string            `msgpack:"domain,omitempty" json:"domain,omitempty"`
	Uptime90D      []bridgeUptimeDay `msgpack:"uptime_90d,omitempty" json:"uptime_90d,omitempty"`
	SortOrder      int               `msgpack:"sort_order,omitempty" json:"sort_order,omitempty"`
	FallbackStatus string            `msgpack:"fallback_status" json:"fallback_status"`
	Critical       bool              `msgpack:"critical" json:"critical"`
	Launched       bool              `msgpack:"launched" json:"launched"`
	LaunchedSet    bool              `msgpack:"launched_set,omitempty" json:"launched_set,omitempty"`
	CreatedAtUnix  int64             `msgpack:"created_at_unix,omitempty" json:"created_at_unix,omitempty"`
	Archived       bool              `msgpack:"archived" json:"archived"`
	ArchivedAtUnix int64             `msgpack:"archived_at_unix,omitempty" json:"archived_at_unix,omitempty"`
	ArchiveBatchID string            `msgpack:"archive_batch_id,omitempty" json:"archive_batch_id,omitempty"`
}

type bridgeUptimeDay struct {
	Date         string
	Status       string
	LegacyStatus string
}

func (value bridgeUptimeDay) MarshalMsgpack() ([]byte, error) {
	if value.Date == "" && value.LegacyStatus != "" {
		return msgpack.Marshal(value.LegacyStatus)
	}
	return msgpack.Marshal(struct {
		Date   string `msgpack:"date"`
		Status string `msgpack:"status"`
	}{Date: value.Date, Status: value.Status})
}

func (value *bridgeUptimeDay) UnmarshalJSON(raw []byte) error {
	var legacy string
	if err := json.Unmarshal(raw, &legacy); err == nil {
		value.Date = ""
		value.Status = ""
		value.LegacyStatus = legacy
		return nil
	}
	var day struct {
		Date   string `json:"date"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &day); err != nil {
		return err
	}
	value.Date = day.Date
	value.Status = day.Status
	value.LegacyStatus = ""
	return nil
}

func (value bridgeUptimeDay) MarshalJSON() ([]byte, error) {
	if value.Date == "" && value.LegacyStatus != "" {
		return json.Marshal(value.LegacyStatus)
	}
	return json.Marshal(struct {
		Date   string `json:"date"`
		Status string `json:"status"`
	}{Date: value.Date, Status: value.Status})
}

type bridgeMonitorSource struct {
	ID            string `msgpack:"id" json:"id"`
	Name          string `msgpack:"name" json:"name"`
	Weight        int    `msgpack:"weight" json:"weight"`
	Kind          string `msgpack:"kind" json:"kind"`
	Trusted       bool   `msgpack:"trusted" json:"trusted"`
	DirectTargets bool   `msgpack:"direct_targets" json:"direct_targets"`
	DefaultTTL    *int64 `msgpack:"default_ttl_seconds" json:"default_ttl_seconds"`
	CreatedAtUnix int64  `msgpack:"created_at_unix,omitempty" json:"created_at_unix,omitempty"`
	Revoked       bool   `msgpack:"revoked" json:"revoked"`
	RevokedAtUnix int64  `msgpack:"revoked_at_unix,omitempty" json:"revoked_at_unix,omitempty"`
}

type bridgeSourceTargetMapping struct {
	ID          string `msgpack:"id" json:"id"`
	SourceID    string `msgpack:"source_id" json:"source_id"`
	RawLabel    string `msgpack:"raw_label" json:"raw_label"`
	ComponentID string `msgpack:"component_id" json:"component_id"`
}

type bridgeObservation struct {
	ID             string `msgpack:"id" json:"id"`
	SourceID       string `msgpack:"source_id" json:"source_id"`
	ComponentID    string `msgpack:"component_id" json:"component_id"`
	Signal         string `msgpack:"signal" json:"signal"`
	Detail         string `msgpack:"detail,omitempty" json:"detail,omitempty"`
	ObservedAtUnix int64  `msgpack:"observed_at_unix" json:"observed_at_unix"`
	ExpiresAtUnix  int64  `msgpack:"expires_at_unix,omitempty" json:"expires_at_unix,omitempty"`
}

type bridgeMonitorIncident struct {
	ID             string   `msgpack:"id" json:"id"`
	Title          string   `msgpack:"title" json:"title"`
	Summary        string   `msgpack:"summary" json:"summary"`
	Status         string   `msgpack:"status" json:"status"`
	Severity       string   `msgpack:"severity" json:"severity"`
	Affects        []string `msgpack:"affects" json:"affects"`
	Auto           bool     `msgpack:"auto" json:"auto"`
	StartedAtUnix  int64    `msgpack:"started_at_unix" json:"started_at_unix"`
	ResolvedAtUnix int64    `msgpack:"resolved_at_unix,omitempty" json:"resolved_at_unix,omitempty"`
	CreatedAtUnix  int64    `msgpack:"created_at_unix,omitempty" json:"created_at_unix,omitempty"`
}

type bridgeIncidentUpdate struct {
	ID         string `msgpack:"id" json:"id"`
	IncidentID string `msgpack:"incident_id" json:"incident_id"`
	AtUnix     int64  `msgpack:"at_unix" json:"at_unix"`
	Label      string `msgpack:"label" json:"label"`
	Body       string `msgpack:"body" json:"body"`
	Author     string `msgpack:"author" json:"author"`
}

type bridgeMaintenance struct {
	ID                 string   `msgpack:"id" json:"id"`
	Title              string   `msgpack:"title" json:"title"`
	Summary            string   `msgpack:"summary" json:"summary"`
	Kind               string   `msgpack:"kind" json:"kind"`
	ScheduledStartUnix int64    `msgpack:"scheduled_start_unix" json:"scheduled_start_unix"`
	ScheduledEndUnix   int64    `msgpack:"scheduled_end_unix" json:"scheduled_end_unix"`
	Affects            []string `msgpack:"affects" json:"affects"`
	CreatedAtUnix      int64    `msgpack:"created_at_unix,omitempty" json:"created_at_unix,omitempty"`
	Cancelled          bool     `msgpack:"cancelled" json:"cancelled"`
	CancelledAtUnix    int64    `msgpack:"cancelled_at_unix,omitempty" json:"cancelled_at_unix,omitempty"`
}

type bridgeIncidentPatch struct {
	ID       string   `msgpack:"id" json:"id"`
	Title    *string  `msgpack:"title,omitempty" json:"title,omitempty"`
	Summary  *string  `msgpack:"summary,omitempty" json:"summary,omitempty"`
	Status   *string  `msgpack:"status,omitempty" json:"status,omitempty"`
	Severity *string  `msgpack:"severity,omitempty" json:"severity,omitempty"`
	Affects  []string `msgpack:"affects,omitempty" json:"affects,omitempty"`
	AtUnix   int64    `msgpack:"at_unix,omitempty" json:"at_unix,omitempty"`
	Author   string   `msgpack:"author,omitempty" json:"author,omitempty"`
	Note     string   `msgpack:"note,omitempty" json:"note,omitempty"`
}

type bridgeComponentPatch struct {
	ID             string             `msgpack:"id" json:"id"`
	ParentID       *string            `msgpack:"parent_id,omitempty" json:"parent_id,omitempty"`
	Name           *string            `msgpack:"name,omitempty" json:"name,omitempty"`
	Kind           *string            `msgpack:"kind,omitempty" json:"kind,omitempty"`
	Tag            *string            `msgpack:"tag,omitempty" json:"tag,omitempty"`
	Brand          *string            `msgpack:"brand,omitempty" json:"brand,omitempty"`
	Domain         *string            `msgpack:"domain,omitempty" json:"domain,omitempty"`
	Uptime90D      *[]bridgeUptimeDay `msgpack:"uptime_90d,omitempty" json:"uptime_90d,omitempty"`
	SortOrder      *int               `msgpack:"sort_order,omitempty" json:"sort_order,omitempty"`
	FallbackStatus *string            `msgpack:"fallback_status,omitempty" json:"fallback_status,omitempty"`
	Critical       *bool              `msgpack:"critical,omitempty" json:"critical,omitempty"`
	Launched       *bool              `msgpack:"launched,omitempty" json:"launched,omitempty"`
}

type bridgeSourcePatch struct {
	ID            string  `msgpack:"id" json:"id"`
	Name          *string `msgpack:"name,omitempty" json:"name,omitempty"`
	Weight        *int    `msgpack:"weight,omitempty" json:"weight,omitempty"`
	Kind          *string `msgpack:"kind,omitempty" json:"kind,omitempty"`
	Trusted       *bool   `msgpack:"trusted,omitempty" json:"trusted,omitempty"`
	DirectTargets *bool   `msgpack:"direct_targets,omitempty" json:"direct_targets,omitempty"`
	DefaultTTL    *int64  `msgpack:"default_ttl_seconds,omitempty" json:"default_ttl_seconds,omitempty"`
	DefaultTTLSet bool    `msgpack:"default_ttl_seconds_set,omitempty" json:"default_ttl_seconds_set,omitempty"`
}

type bridgeMaintenancePatch struct {
	ID                 string   `msgpack:"id" json:"id"`
	Title              *string  `msgpack:"title,omitempty" json:"title,omitempty"`
	Summary            *string  `msgpack:"summary,omitempty" json:"summary,omitempty"`
	Kind               *string  `msgpack:"kind,omitempty" json:"kind,omitempty"`
	ScheduledStartUnix *int64   `msgpack:"scheduled_start_unix,omitempty" json:"scheduled_start_unix,omitempty"`
	ScheduledEndUnix   *int64   `msgpack:"scheduled_end_unix,omitempty" json:"scheduled_end_unix,omitempty"`
	Affects            []string `msgpack:"affects,omitempty" json:"affects,omitempty"`
}

type bridgeIngestRequest struct {
	ObservationID  string `msgpack:"observation_id" json:"observation_id"`
	SourceID       string `msgpack:"source_id" json:"source_id"`
	RawLabel       string `msgpack:"raw_label" json:"raw_label"`
	ComponentID    string `msgpack:"component_id,omitempty" json:"component_id,omitempty"`
	Signal         string `msgpack:"signal" json:"signal"`
	Detail         string `msgpack:"detail,omitempty" json:"detail,omitempty"`
	ObservedAtUnix int64  `msgpack:"observed_at_unix,omitempty" json:"observed_at_unix,omitempty"`
	ExpiresAtUnix  int64  `msgpack:"expires_at_unix,omitempty" json:"expires_at_unix,omitempty"`
}

type bridgeMonitorCommand struct {
	Version          string                     `msgpack:"version" json:"version"`
	ID               string                     `msgpack:"id" json:"id"`
	Kind             string                     `msgpack:"kind" json:"kind"`
	AtUnix           int64                      `msgpack:"at_unix" json:"at_unix"`
	ImportMode       bool                       `msgpack:"import_mode,omitempty" json:"import_mode,omitempty"`
	Component        *bridgeMonitorComponent    `msgpack:"component,omitempty" json:"component,omitempty"`
	ComponentPatch   *bridgeComponentPatch      `msgpack:"component_patch,omitempty" json:"component_patch,omitempty"`
	ComponentID      string                     `msgpack:"component_id,omitempty" json:"component_id,omitempty"`
	ArchiveBatchID   string                     `msgpack:"archive_batch_id,omitempty" json:"archive_batch_id,omitempty"`
	Source           *bridgeMonitorSource       `msgpack:"source,omitempty" json:"source,omitempty"`
	SourcePatch      *bridgeSourcePatch         `msgpack:"source_patch,omitempty" json:"source_patch,omitempty"`
	SourceID         string                     `msgpack:"source_id,omitempty" json:"source_id,omitempty"`
	Mapping          *bridgeSourceTargetMapping `msgpack:"mapping,omitempty" json:"mapping,omitempty"`
	MappingID        string                     `msgpack:"mapping_id,omitempty" json:"mapping_id,omitempty"`
	Observation      *bridgeObservation         `msgpack:"observation,omitempty" json:"observation,omitempty"`
	Ingest           *bridgeIngestRequest       `msgpack:"ingest,omitempty" json:"ingest,omitempty"`
	Incident         *bridgeMonitorIncident     `msgpack:"incident,omitempty" json:"incident,omitempty"`
	IncidentID       string                     `msgpack:"incident_id,omitempty" json:"incident_id,omitempty"`
	IncidentPatch    *bridgeIncidentPatch       `msgpack:"incident_patch,omitempty" json:"incident_patch,omitempty"`
	Update           *bridgeIncidentUpdate      `msgpack:"update,omitempty" json:"update,omitempty"`
	Maintenance      *bridgeMaintenance         `msgpack:"maintenance,omitempty" json:"maintenance,omitempty"`
	MaintenancePatch *bridgeMaintenancePatch    `msgpack:"maintenance_patch,omitempty" json:"maintenance_patch,omitempty"`
	MaintenanceID    string                     `msgpack:"maintenance_id,omitempty" json:"maintenance_id,omitempty"`
}

type bridgeMonitorQuery struct {
	Version             string `msgpack:"version" json:"version"`
	ComponentID         string `msgpack:"component_id,omitempty" json:"component_id,omitempty"`
	SourceID            string `msgpack:"source_id,omitempty" json:"source_id,omitempty"`
	IncidentID          string `msgpack:"incident_id,omitempty" json:"incident_id,omitempty"`
	MaintenanceID       string `msgpack:"maintenance_id,omitempty" json:"maintenance_id,omitempty"`
	IncludeArchived     bool   `msgpack:"include_archived,omitempty" json:"include_archived,omitempty"`
	IncludeObservations bool   `msgpack:"include_observations,omitempty" json:"include_observations,omitempty"`
	AtUnix              int64  `msgpack:"at_unix" json:"at_unix"`
}

type bridgeSubscribeRequest struct {
	Version             string    `msgpack:"version" json:"version"`
	RequestID           string    `msgpack:"request_id" json:"request_id"`
	Email               string    `msgpack:"email" json:"email"`
	ConfirmationToken   string    `msgpack:"confirmation_token" json:"confirmation_token"`
	UnsubscribeToken    string    `msgpack:"unsubscribe_token" json:"unsubscribe_token"`
	ConfirmationSubject string    `msgpack:"confirmation_subject,omitempty" json:"confirmation_subject,omitempty"`
	ConfirmationBody    string    `msgpack:"confirmation_body,omitempty" json:"confirmation_body,omitempty"`
	RequestedAt         time.Time `msgpack:"requested_at" json:"requested_at"`
}

type bridgeConfirmRequest struct {
	Version   string `msgpack:"version" json:"version"`
	RequestID string `msgpack:"request_id" json:"request_id"`
	Token     string `msgpack:"token" json:"token"`
}

type bridgeUnsubscribeRequest = bridgeConfirmRequest

type bridgeConfirmationResendRequest struct {
	Version             string    `msgpack:"version" json:"version"`
	RequestID           string    `msgpack:"request_id" json:"request_id"`
	Email               string    `msgpack:"email" json:"email"`
	ConfirmationToken   string    `msgpack:"confirmation_token" json:"confirmation_token"`
	ConfirmationSubject string    `msgpack:"confirmation_subject,omitempty" json:"confirmation_subject,omitempty"`
	ConfirmationBody    string    `msgpack:"confirmation_body,omitempty" json:"confirmation_body,omitempty"`
	RequestedAt         time.Time `msgpack:"requested_at" json:"requested_at"`
}

type bridgeSubscriberAdminListRequest struct {
	Version string `msgpack:"version" json:"version"`
}

type bridgeSubscriberAdminGetRequest struct {
	Version      string `msgpack:"version" json:"version"`
	SubscriberID string `msgpack:"subscriber_id" json:"subscriber_id"`
}

type bridgeSubscriberAdminDeleteRequest struct {
	Version      string `msgpack:"version" json:"version"`
	RequestID    string `msgpack:"request_id" json:"request_id"`
	SubscriberID string `msgpack:"subscriber_id" json:"subscriber_id"`
}

type bridgeSubscriberAdminStateSetRequest struct {
	Version      string    `msgpack:"version" json:"version"`
	RequestID    string    `msgpack:"request_id" json:"request_id"`
	SubscriberID string    `msgpack:"subscriber_id" json:"subscriber_id"`
	State        string    `msgpack:"state" json:"state"`
	ChangedAt    time.Time `msgpack:"changed_at" json:"changed_at"`
}

type bridgeLegacySubscriberRow struct {
	ID          string     `msgpack:"id" json:"id"`
	Email       string     `msgpack:"email" json:"email"`
	ConfirmedAt *time.Time `msgpack:"confirmed_at,omitempty" json:"confirmedAt,omitempty"`
	CreatedAt   time.Time  `msgpack:"created_at" json:"createdAt"`
}

type bridgeSubscriberMigrationImportRequest struct {
	Version     string                      `msgpack:"version" json:"version"`
	RequestID   string                      `msgpack:"request_id" json:"request_id"`
	Subscribers []bridgeLegacySubscriberRow `msgpack:"subscribers" json:"subscribers"`
}

type bridgeNotificationRequest struct {
	Version    string    `msgpack:"version" json:"version"`
	RequestID  string    `msgpack:"request_id" json:"request_id"`
	EventID    string    `msgpack:"event_id" json:"event_id"`
	Subject    string    `msgpack:"subject" json:"subject"`
	Body       string    `msgpack:"body" json:"body"`
	OccurredAt time.Time `msgpack:"occurred_at" json:"occurred_at"`
}

type bridgePublishRequest struct {
	MonitorRequest      bridgeMonitorCommand      `json:"monitor_request"`
	NotificationRequest bridgeNotificationRequest `json:"notification_request"`
}

type bridgePublishResponse struct {
	Monitor      any `json:"monitor"`
	Notification any `json:"notification"`
}
