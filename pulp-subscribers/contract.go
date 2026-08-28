package main

import "time"

// ContractVersion changes only for a deliberate wire incompatibility.
const ContractVersion = "subscription-outbox/v1"

const (
	FnCatalog            = "subscription-outbox.v1.catalog"
	FnSubscribe          = "subscription-outbox.v1.subscribe"
	FnConfirm            = "subscription-outbox.v1.confirm"
	FnUnsubscribe        = "subscription-outbox.v1.unsubscribe"
	FnNotifyIncident     = "subscription-outbox.v1.notify.incident"
	FnNotifyMaintenance  = "subscription-outbox.v1.notify.maintenance"
	FnProjection         = "subscription-outbox.v1.projection"
	FnConfirmationResend = "subscription-outbox.v1.confirmation.resend"
	FnAdminList          = "subscription-outbox.v1.admin.list"
	FnAdminGet           = "subscription-outbox.v1.admin.get"
	FnAdminDelete        = "subscription-outbox.v1.admin.delete"
	FnAdminStateSet      = "subscription-outbox.v1.admin.state.set"
	FnMigrationImport    = "subscription-outbox.v1.migration.import"
	FnDeliveryConfigSet  = "subscription-outbox.v1.delivery.config.set"
	FnTransitionApply    = "subscription-outbox.v1.transition.apply"
	FnOutboxClaim        = "subscription-outbox.v1.outbox.claim"
	FnOutboxReceiptApply = "subscription-outbox.v1.outbox.receipt.apply"
)

type Catalog struct {
	Version   string   `msgpack:"version" json:"version"`
	Commands  []string `msgpack:"commands" json:"commands"`
	Queries   []string `msgpack:"queries" json:"queries"`
	OutboxABI string   `msgpack:"outbox_abi" json:"outbox_abi"`
}

// SubscribeRequest is intentionally unauthenticated: possession of the
// confirmation token is required before the address can receive alerts.
// RequestID, ConfirmationToken, and UnsubscribeToken are caller-generated
// opaque values and must be stable across a retry.
type SubscribeRequest struct {
	Version             string    `msgpack:"version" json:"version"`
	RequestID           string    `msgpack:"request_id" json:"request_id"`
	Email               string    `msgpack:"email" json:"email"`
	ConfirmationToken   string    `msgpack:"confirmation_token" json:"confirmation_token"`
	UnsubscribeToken    string    `msgpack:"unsubscribe_token" json:"unsubscribe_token"`
	ConfirmationSubject string    `msgpack:"confirmation_subject,omitempty" json:"confirmation_subject,omitempty"`
	ConfirmationBody    string    `msgpack:"confirmation_body,omitempty" json:"confirmation_body,omitempty"`
	RequestedAt         time.Time `msgpack:"requested_at" json:"requested_at"`
}

type ConfirmRequest struct {
	Version   string `msgpack:"version" json:"version"`
	RequestID string `msgpack:"request_id" json:"request_id"`
	Token     string `msgpack:"token" json:"token"`
}

type UnsubscribeRequest struct {
	Version   string `msgpack:"version" json:"version"`
	RequestID string `msgpack:"request_id" json:"request_id"`
	Token     string `msgpack:"token" json:"token"`
}

type NotificationRequest struct {
	Version            string    `msgpack:"version" json:"version"`
	RequestID          string    `msgpack:"request_id" json:"request_id"`
	EventID            string    `msgpack:"event_id" json:"event_id"`
	Subject            string    `msgpack:"subject" json:"subject"`
	Body               string    `msgpack:"body" json:"body"`
	UnsubscribeBaseURL string    `msgpack:"unsubscribe_base_url,omitempty" json:"unsubscribe_base_url,omitempty"`
	OccurredAt         time.Time `msgpack:"occurred_at" json:"occurred_at"`
}

type CommandResult struct {
	Version      string `msgpack:"version" json:"version"`
	SubscriberID string `msgpack:"subscriber_id,omitempty" json:"subscriber_id,omitempty"`
	Created      bool   `msgpack:"created,omitempty" json:"created,omitempty"`
	Confirmed    bool   `msgpack:"confirmed,omitempty" json:"confirmed,omitempty"`
	Unsubscribed bool   `msgpack:"unsubscribed,omitempty" json:"unsubscribed,omitempty"`
	IntentCount  int    `msgpack:"intent_count,omitempty" json:"intent_count,omitempty"`
}

// Projection deliberately contains no recipient address, token, outbox body,
// or host receipt. Those remain private owner state.
type Projection struct {
	Version            string `msgpack:"version" json:"version"`
	PendingCount       int    `msgpack:"pending_count" json:"pending_count"`
	ConfirmedCount     int    `msgpack:"confirmed_count" json:"confirmed_count"`
	UnsubscribedCount  int    `msgpack:"unsubscribed_count" json:"unsubscribed_count"`
	PendingIntentCount int    `msgpack:"pending_intent_count" json:"pending_intent_count"`
}

// ConfirmationResendRequest is the explicit, idempotent resend path. Public
// callers receive only Accepted, so an address's existence or state is never
// disclosed.
type ConfirmationResendRequest struct {
	Version             string    `msgpack:"version" json:"version"`
	RequestID           string    `msgpack:"request_id" json:"request_id"`
	Email               string    `msgpack:"email" json:"email"`
	ConfirmationToken   string    `msgpack:"confirmation_token" json:"confirmation_token"`
	ConfirmationSubject string    `msgpack:"confirmation_subject,omitempty" json:"confirmation_subject,omitempty"`
	ConfirmationBody    string    `msgpack:"confirmation_body,omitempty" json:"confirmation_body,omitempty"`
	RequestedAt         time.Time `msgpack:"requested_at" json:"requested_at"`
}

type ConfirmationResendResult struct {
	Version  string `msgpack:"version" json:"version"`
	Accepted bool   `msgpack:"accepted" json:"accepted"`
}

// Admin providers are deliberately separate from Projection. They contain PII
// and must only be routed by the host-authenticated internal bridge.
type AdminListRequest struct {
	Version string `msgpack:"version" json:"version"`
}

type AdminGetRequest struct {
	Version      string `msgpack:"version" json:"version"`
	SubscriberID string `msgpack:"subscriber_id" json:"subscriber_id"`
}

type AdminSubscriber struct {
	ID          string     `msgpack:"id" json:"id"`
	Email       string     `msgpack:"email" json:"email"`
	State       string     `msgpack:"state" json:"state"`
	ConfirmedAt *time.Time `msgpack:"confirmed_at,omitempty" json:"confirmedAt"`
	CreatedAt   time.Time  `msgpack:"created_at" json:"createdAt"`
}

type AdminSubscriberList struct {
	Version     string            `msgpack:"version" json:"version"`
	Subscribers []AdminSubscriber `msgpack:"subscribers" json:"subscribers"`
}

type AdminSubscriberGet struct {
	Version    string           `msgpack:"version" json:"version"`
	Found      bool             `msgpack:"found" json:"found"`
	Subscriber *AdminSubscriber `msgpack:"subscriber,omitempty" json:"subscriber,omitempty"`
}

type AdminDeleteRequest struct {
	Version      string `msgpack:"version" json:"version"`
	RequestID    string `msgpack:"request_id" json:"request_id"`
	SubscriberID string `msgpack:"subscriber_id" json:"subscriber_id"`
}

type AdminStateSetRequest struct {
	Version      string    `msgpack:"version" json:"version"`
	RequestID    string    `msgpack:"request_id" json:"request_id"`
	SubscriberID string    `msgpack:"subscriber_id" json:"subscriber_id"`
	State        string    `msgpack:"state" json:"state"`
	ChangedAt    time.Time `msgpack:"changed_at" json:"changed_at"`
}

type AdminMutationResult struct {
	Version string `msgpack:"version" json:"version"`
	Found   bool   `msgpack:"found" json:"found"`
	Changed bool   `msgpack:"changed" json:"changed"`
	State   string `msgpack:"state,omitempty" json:"state,omitempty"`
}

// LegacySubscriberRow is the complete legacy Postgres subscriber shape.
// Importing preserves the legacy ID as both confirmation and unsubscribe
// token, so outstanding links remain valid after the state-owner cutover.
type LegacySubscriberRow struct {
	ID          string     `msgpack:"id" json:"id"`
	Email       string     `msgpack:"email" json:"email"`
	ConfirmedAt *time.Time `msgpack:"confirmed_at,omitempty" json:"confirmedAt"`
	CreatedAt   time.Time  `msgpack:"created_at" json:"createdAt"`
}

type MigrationImportRequest struct {
	Version     string                `msgpack:"version" json:"version"`
	RequestID   string                `msgpack:"request_id" json:"request_id"`
	Subscribers []LegacySubscriberRow `msgpack:"subscribers" json:"subscribers"`
}

type MigrationImportReceipt struct {
	Version   string `msgpack:"version" json:"version"`
	RequestID string `msgpack:"request_id" json:"request_id"`
	Imported  int    `msgpack:"imported" json:"imported"`
	Unchanged int    `msgpack:"unchanged" json:"unchanged"`
}

type DeliveryConfigSetRequest struct {
	Version            string `msgpack:"version" json:"version"`
	RequestID          string `msgpack:"request_id" json:"request_id"`
	UnsubscribeBaseURL string `msgpack:"unsubscribe_base_url" json:"unsubscribe_base_url"`
}

type DeliveryConfigSetResult struct {
	Version            string `msgpack:"version" json:"version"`
	Applied            bool   `msgpack:"applied" json:"applied"`
	UnsubscribeBaseURL string `msgpack:"unsubscribe_base_url" json:"unsubscribe_base_url"`
}

// MonitorCommandResult deliberately mirrors monitor.v1 CommandResult. The
// provider decodes the monitor result bytes directly, ignoring monitor fields
// it does not need, so Lua never has to decode or rebuild the envelope.
type MonitorCommandResult struct {
	Version     string             `msgpack:"version" json:"version"`
	CommandID   string             `msgpack:"command_id" json:"command_id"`
	Revision    uint64             `msgpack:"revision" json:"revision"`
	Deduped     bool               `msgpack:"deduped" json:"deduped"`
	Transitions []DomainTransition `msgpack:"transitions,omitempty" json:"transitions,omitempty"`
}

type DomainTransition struct {
	ID                   string                    `msgpack:"id" json:"id"`
	Kind                 string                    `msgpack:"kind" json:"kind"`
	EntityID             string                    `msgpack:"entity_id" json:"entity_id"`
	ComponentID          string                    `msgpack:"component_id,omitempty" json:"component_id,omitempty"`
	AffectedComponentIDs []string                  `msgpack:"affected_component_ids,omitempty" json:"affected_component_ids,omitempty"`
	Status               string                    `msgpack:"status,omitempty" json:"status,omitempty"`
	PreviousStatus       string                    `msgpack:"previous_status,omitempty" json:"previous_status,omitempty"`
	Severity             string                    `msgpack:"severity,omitempty" json:"severity,omitempty"`
	PreviousSeverity     string                    `msgpack:"previous_severity,omitempty" json:"previous_severity,omitempty"`
	AtUnix               int64                     `msgpack:"at_unix,omitempty" json:"at_unix,omitempty"`
	Incident             *TransitionIncident       `msgpack:"incident,omitempty" json:"incident,omitempty"`
	IncidentUpdate       *TransitionIncidentUpdate `msgpack:"incident_update,omitempty" json:"incident_update,omitempty"`
	Maintenance          *TransitionMaintenance    `msgpack:"maintenance,omitempty" json:"maintenance,omitempty"`
}

type TransitionIncident struct {
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

type TransitionIncidentUpdate struct {
	ID         string `msgpack:"id" json:"id"`
	IncidentID string `msgpack:"incident_id" json:"incident_id"`
	AtUnix     int64  `msgpack:"at_unix" json:"at_unix"`
	Label      string `msgpack:"label" json:"label"`
	Body       string `msgpack:"body" json:"body"`
	Author     string `msgpack:"author" json:"author"`
}

type TransitionMaintenance struct {
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

type TransitionApplyResult struct {
	Version         string `msgpack:"version" json:"version"`
	MonitorCommand  string `msgpack:"monitor_command_id" json:"monitor_command_id"`
	TransitionCount int    `msgpack:"transition_count" json:"transition_count"`
	IntentCount     int    `msgpack:"intent_count" json:"intent_count"`
	Suppressed      bool   `msgpack:"suppressed" json:"suppressed"`
}

type OutboxClaimRequest struct {
	Version  string `msgpack:"version" json:"version"`
	WorkerID string `msgpack:"worker_id" json:"worker_id"`
	Limit    int    `msgpack:"limit" json:"limit"`
}

// OutboxIntent is a private host-effect contract. It is never part of the
// public projection; the host needs Recipient to perform email delivery.
type OutboxIntent struct {
	Version        string `msgpack:"version" json:"version"`
	IntentID       string `msgpack:"intent_id" json:"intent_id"`
	IdempotencyKey string `msgpack:"idempotency_key" json:"idempotency_key"`
	Kind           string `msgpack:"kind" json:"kind"`
	Recipient      string `msgpack:"recipient" json:"recipient"`
	Subject        string `msgpack:"subject" json:"subject"`
	Body           string `msgpack:"body" json:"body"`
}

type OutboxClaim struct {
	Version string         `msgpack:"version" json:"version"`
	Intents []OutboxIntent `msgpack:"intents" json:"intents"`
}

type ReceiptStatus string

const (
	ReceiptDelivered ReceiptStatus = "delivered"
	ReceiptFailed    ReceiptStatus = "failed"
)

type OutboxReceipt struct {
	Version  string        `msgpack:"version" json:"version"`
	IntentID string        `msgpack:"intent_id" json:"intent_id"`
	Status   ReceiptStatus `msgpack:"status" json:"status"`
	Detail   string        `msgpack:"detail,omitempty" json:"detail,omitempty"`
}

type ReceiptApplyResult struct {
	Version string `msgpack:"version" json:"version"`
	Applied bool   `msgpack:"applied" json:"applied"`
}
