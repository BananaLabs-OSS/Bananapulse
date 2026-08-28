package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BananaLabs-OSS/Pulp/run"
)

type providerObserver struct {
	ready chan run.ApplicationProviderAccess
}

func (o *providerObserver) AfterApplicationStart(context.Context, run.ApplicationIdentity) error {
	return nil
}

func (o *providerObserver) AfterApplicationStartWithProvider(
	_ context.Context,
	_ run.ApplicationIdentity,
	access run.ApplicationProviderAccess,
) error {
	o.ready <- access
	return nil
}

func (o *providerObserver) BeforeApplicationShutdown(context.Context, run.ApplicationIdentity) error {
	return nil
}

type monitorCommand struct {
	Version   string            `msgpack:"version"`
	ID        string            `msgpack:"id"`
	Kind      string            `msgpack:"kind"`
	AtUnix    int64             `msgpack:"at_unix"`
	Component *monitorComponent `msgpack:"component,omitempty"`
	Incident  *monitorIncident  `msgpack:"incident,omitempty"`
}

type monitorComponent struct {
	ID             string `msgpack:"id"`
	Name           string `msgpack:"name"`
	Kind           string `msgpack:"kind"`
	FallbackStatus string `msgpack:"fallback_status"`
	Critical       bool   `msgpack:"critical"`
}

type monitorIncident struct {
	ID            string   `msgpack:"id"`
	Title         string   `msgpack:"title"`
	Summary       string   `msgpack:"summary"`
	Status        string   `msgpack:"status"`
	Severity      string   `msgpack:"severity"`
	Affects       []string `msgpack:"affects"`
	StartedAtUnix int64    `msgpack:"started_at_unix"`
}

type monitorCommandResult struct {
	Version   string `msgpack:"version" json:"version"`
	CommandID string `msgpack:"command_id" json:"command_id"`
	Revision  uint64 `msgpack:"revision" json:"revision"`
	Deduped   bool   `msgpack:"deduped" json:"deduped"`
}

type subscribeRequest struct {
	Version           string    `msgpack:"version"`
	RequestID         string    `msgpack:"request_id"`
	Email             string    `msgpack:"email"`
	ConfirmationToken string    `msgpack:"confirmation_token"`
	UnsubscribeToken  string    `msgpack:"unsubscribe_token"`
	RequestedAt       time.Time `msgpack:"requested_at"`
}

type confirmRequest struct {
	Version   string `msgpack:"version"`
	RequestID string `msgpack:"request_id"`
	Token     string `msgpack:"token"`
}

type notificationRequest struct {
	Version    string    `msgpack:"version"`
	RequestID  string    `msgpack:"request_id"`
	EventID    string    `msgpack:"event_id"`
	Subject    string    `msgpack:"subject"`
	Body       string    `msgpack:"body"`
	OccurredAt time.Time `msgpack:"occurred_at"`
}

type subscriberCommandResult struct {
	Version     string `msgpack:"version" json:"version"`
	Confirmed   bool   `msgpack:"confirmed" json:"confirmed"`
	IntentCount int    `msgpack:"intent_count" json:"intent_count"`
}

type subscriberProjection struct {
	Version            string `msgpack:"version" json:"version"`
	ConfirmedCount     int    `msgpack:"confirmed_count" json:"confirmed_count"`
	PendingIntentCount int    `msgpack:"pending_intent_count" json:"pending_intent_count"`
}

type recordingSender struct {
	mu      sync.Mutex
	intents []emailIntent
}

type transientSender struct {
	attempts int
}

func (s *transientSender) Send(context.Context, emailIntent) error {
	s.attempts++
	if s.attempts == 1 {
		return &deliveryError{err: fmt.Errorf("temporary provider outage"), retryable: true}
	}
	return nil
}

func (s *recordingSender) Send(_ context.Context, intent emailIntent) error {
	if intent.IdempotencyKey == "" {
		return fmt.Errorf("missing idempotency key")
	}
	s.mu.Lock()
	s.intents = append(s.intents, intent)
	s.mu.Unlock()
	return nil
}

func TestRealPulpLuaOwnersAndDurableEmailReceipt(t *testing.T) {
	observer := &providerObserver{ready: make(chan run.ApplicationProviderAccess, 1)}
	appPath := filepath.Clean(filepath.Join("..", "application", "pulp.app.toml"))
	runtime, err := run.NewDirectApplicationRuntime(appPath, run.DirectApplicationOptions{
		StorageRoot: t.TempDir(),
		Lifecycle:   observer,
	})
	if err != nil {
		t.Fatalf("create Bananapulse runtime: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runtime.Start(ctx); err != nil {
		t.Fatalf("start Bananapulse runtime: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := runtime.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown Bananapulse runtime: %v", err)
		}
	})
	access := <-observer.ready
	client, err := newApplicationClient(access)
	if err != nil {
		t.Fatal(err)
	}
	if err := configureSubscriberDelivery(ctx, client, "https://status.example.test/unsubscribe"); err != nil {
		t.Fatalf("configure subscriber delivery: %v", err)
	}

	now := time.Unix(1_800_000_000, 0).UTC()
	var subscribed subscriberCommandResult
	if err := client.callRaw(ctx, eventSubscriberSubscribe, subscribeRequest{
		Version:           subscriberContractVersion,
		RequestID:         "subscribe-1",
		Email:             "owner@example.test",
		ConfirmationToken: "confirm-1",
		UnsubscribeToken:  "unsubscribe-1",
		RequestedAt:       now,
	}, &subscribed); err != nil {
		t.Fatalf("subscribe through Lua: %v", err)
	}
	var confirmed subscriberCommandResult
	if err := client.callRaw(ctx, eventSubscriberConfirm, confirmRequest{
		Version: subscriberContractVersion, RequestID: "confirm-request-1", Token: "confirm-1",
	}, &confirmed); err != nil {
		t.Fatalf("confirm through Lua: %v", err)
	}
	if !confirmed.Confirmed {
		t.Fatal("subscriber was not confirmed")
	}

	var componentResult monitorCommandResult
	if err := client.callRaw(ctx, eventMonitorCommand, monitorCommand{
		Version: "monitor.v1",
		ID:      "component-command-1",
		Kind:    "upsert_component",
		AtUnix:  now.Unix(),
		Component: &monitorComponent{
			ID:             "database",
			Name:           "Database",
			Kind:           "service",
			FallbackStatus: "operational",
			Critical:       true,
		},
	}, &componentResult); err != nil {
		t.Fatalf("create monitor component through Lua: %v", err)
	}

	command := monitorCommand{
		Version: "monitor.v1",
		ID:      "incident-command-1",
		Kind:    "open_incident",
		AtUnix:  now.Unix(),
		Incident: &monitorIncident{
			ID:            "incident-1",
			Title:         "Database latency",
			Summary:       "Elevated write latency",
			Status:        "investigating",
			Severity:      "major",
			Affects:       []string{"database"},
			StartedAtUnix: now.Unix(),
		},
	}
	notification := notificationRequest{
		Version:    subscriberContractVersion,
		RequestID:  "notify-request-1",
		EventID:    "incident-1:opened",
		Subject:    "Investigating database latency",
		Body:       "Elevated write latency",
		OccurredAt: now,
	}
	var monitorResult monitorCommandResult
	var notificationResult subscriberCommandResult
	if err := client.publish(
		ctx,
		eventIncidentPublish,
		command,
		notification,
		&monitorResult,
		&notificationResult,
	); err != nil {
		t.Fatalf("publish incident through Lua: %v", err)
	}
	if monitorResult.CommandID != command.ID || monitorResult.Revision == 0 {
		t.Fatalf("monitor result = %#v", monitorResult)
	}
	if notificationResult.IntentCount != 1 {
		t.Fatalf("notification result = %#v, want one incident intent", notificationResult)
	}

	sender := &recordingSender{}
	worker, err := newEmailOutboxWorker(client, sender)
	if err != nil {
		t.Fatal(err)
	}
	drained, err := worker.DrainOnce(ctx, "e2e-worker", 10)
	if err != nil {
		t.Fatalf("drain email effects: %v", err)
	}
	// Subscribe creates a confirmation intent; publishing creates an incident
	// intent. Both must cross the host boundary and receive durable receipts.
	if drained.Claimed != 2 || drained.Delivered != 2 || drained.Failed != 0 {
		t.Fatalf("drain result = %#v, want two delivered", drained)
	}
	if len(sender.intents) != 2 {
		t.Fatalf("host sender received %d intents, want two", len(sender.intents))
	}

	var projection subscriberProjection
	if err := client.callRaw(ctx, eventSubscriberProjection, nil, &projection); err != nil {
		t.Fatalf("subscriber projection through Lua: %v", err)
	}
	if projection.ConfirmedCount != 1 || projection.PendingIntentCount != 0 {
		t.Fatalf("subscriber projection = %#v", projection)
	}
	drained, err = worker.DrainOnce(ctx, "e2e-worker", 10)
	if err != nil {
		t.Fatalf("second email drain: %v", err)
	}
	if drained.Claimed != 0 {
		t.Fatalf("receipt did not settle outbox: %#v", drained)
	}

	var retrySubscribed subscriberCommandResult
	if err := client.callRaw(ctx, eventSubscriberSubscribe, subscribeRequest{
		Version:           subscriberContractVersion,
		RequestID:         "subscribe-retry",
		Email:             "retry@example.test",
		ConfirmationToken: "confirm-retry",
		UnsubscribeToken:  "unsubscribe-retry",
		RequestedAt:       now,
	}, &retrySubscribed); err != nil {
		t.Fatalf("subscribe retry recipient through Lua: %v", err)
	}
	flaky := &transientSender{}
	retryWorker, err := newEmailOutboxWorker(client, flaky)
	if err != nil {
		t.Fatal(err)
	}
	firstRetry, err := retryWorker.DrainOnce(ctx, "retry-worker", 10)
	if err != nil {
		t.Fatalf("first retry drain: %v", err)
	}
	if firstRetry.Claimed != 1 || firstRetry.Failed != 1 {
		t.Fatalf("first retry drain = %#v", firstRetry)
	}
	secondRetry, err := retryWorker.DrainOnce(ctx, "retry-worker", 10)
	if err != nil {
		t.Fatalf("second retry drain: %v", err)
	}
	if secondRetry.Claimed != 1 || secondRetry.Delivered != 1 || flaky.attempts != 2 {
		t.Fatalf("second retry drain = %#v, attempts=%d", secondRetry, flaky.attempts)
	}
}

func TestHTTPBridgeRunsRealPulpLuaWASMComposition(t *testing.T) {
	observer := &providerObserver{ready: make(chan run.ApplicationProviderAccess, 1)}
	appPath := filepath.Clean(filepath.Join("..", "application", "pulp.app.toml"))
	runtime, err := run.NewDirectApplicationRuntime(appPath, run.DirectApplicationOptions{
		StorageRoot: t.TempDir(),
		Lifecycle:   observer,
	})
	if err != nil {
		t.Fatalf("create Bananapulse runtime: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runtime.Start(ctx); err != nil {
		t.Fatalf("start Bananapulse runtime: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := runtime.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown Bananapulse runtime: %v", err)
		}
	})
	client, err := newApplicationClient(<-observer.ready)
	if err != nil {
		t.Fatal(err)
	}
	if err := configureSubscriberDelivery(ctx, client, "https://status.example.test/unsubscribe"); err != nil {
		t.Fatalf("configure subscriber delivery: %v", err)
	}
	bridge, err := newHTTPBridgeWithFamilies(client, "bridge-test-token", bridgeFamilies{
		monitorAdmin: true, monitorIngest: true, monitorSweep: true,
		subscriberAdmin: true, migration: true, auth: true, authAdmin: true, sourceAdmin: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceLifecycle, err := newSourceLifecycleService(client, newMemorySourceSagaStore())
	if err != nil {
		t.Fatal(err)
	}
	bridge.sources = sourceLifecycle
	server := httptest.NewServer(bridge.Handler())
	t.Cleanup(server.Close)

	now := time.Unix(1_800_000_100, 0).UTC()
	component := bridgeMonitorCommand{
		Version: "monitor.v1", ID: "bridge-component-1", Kind: "upsert_component", AtUnix: now.Unix(),
		Component: &bridgeMonitorComponent{
			ID: "bridge-database", Name: "Bridge Database", Kind: "service", Tag: "database", SortOrder: 10,
			FallbackStatus: "operational", Critical: true,
		},
	}
	var commandResult monitorCommandResult
	postBridgeEvent(t, server.URL, "bridge-test-token", eventMonitorAdminCommand, component, &commandResult)
	if commandResult.CommandID != component.ID || commandResult.Revision == 0 {
		t.Fatalf("command result = %#v", commandResult)
	}
	defaultTTL := int64(60)
	var sourceCreated sourceLifecycleResult
	postBridgeEvent(t, server.URL, "bridge-test-token", eventHostSourceAdminCreate, sourceAdminCreateRequest{
		Version: sourceLifecycleContractVersion, RequestID: "bridge-source-create-1",
		Source: bridgeMonitorSource{
			ID: "bridge-vendor", Name: "Bridge Vendor", Weight: 1, Kind: "push",
			Trusted: true, DefaultTTL: &defaultTTL,
		},
		CredentialID: "bridge-vendor-credential", Token: "bridge-vendor-test-token",
		ActorID: "bridge-admin", CreatedAt: now,
	}, &sourceCreated)
	if !sourceCreated.Completed || sourceCreated.SourceID != "bridge-vendor" {
		t.Fatalf("source lifecycle create = %#v", sourceCreated)
	}
	var sourceValidated struct {
		Valid    bool   `json:"valid"`
		SourceID string `json:"source_id"`
	}
	postBridgeEvent(t, server.URL, "bridge-test-token", eventHostAuthSourceValidate, bridgeAuthSourceCredentialValidateRequest{
		Version: "bananapulse.auth/v1", RequestID: "bridge-source-validate-1",
		Token: "bridge-vendor-test-token", ValidatedAt: now,
	}, &sourceValidated)
	if !sourceValidated.Valid || sourceValidated.SourceID != "bridge-vendor" {
		t.Fatalf("source credential validation = %#v", sourceValidated)
	}
	mapping := bridgeMonitorCommand{
		Version: "monitor.v1", ID: "bridge-mapping-1", Kind: "map_source_target", AtUnix: now.Unix(),
		Mapping: &bridgeSourceTargetMapping{
			ID: "bridge-vendor/database", SourceID: "bridge-vendor",
			RawLabel: "database", ComponentID: "bridge-database",
		},
	}
	postBridgeEvent(t, server.URL, "bridge-test-token", eventMonitorAdminCommand, mapping, &commandResult)
	var ingestResult map[string]any
	postBridgeEvent(t, server.URL, "bridge-test-token", eventMonitorIngestAuthenticated, bridgeMonitorCommand{
		Version: "monitor.v1", ID: "bridge-ingest-1", Kind: "ingest_observation", AtUnix: now.Unix(),
		Ingest: &bridgeIngestRequest{
			ObservationID: "bridge-observation-1", SourceID: "bridge-vendor", RawLabel: "database",
			Signal: "ok", ObservedAtUnix: now.Unix(),
		},
	}, &ingestResult)
	if ingestResult["evaluation"] == nil {
		t.Fatalf("authenticated ingest lost evaluation: %#v", ingestResult)
	}
	var sweepResult map[string]any
	postBridgeEvent(t, server.URL, "bridge-test-token", eventMonitorSweep, bridgeMonitorCommand{
		Version: "monitor.v1", ID: "bridge-sweep-1", Kind: "sweep_reconcile", AtUnix: now.Add(time.Minute).Unix(),
	}, &sweepResult)
	if sweepResult["sweep"] == nil {
		t.Fatalf("sweep lost reconciliation result: %#v", sweepResult)
	}
	var migrationResult monitorCommandResult
	postBridgeEvent(t, server.URL, "bridge-test-token", eventMonitorMigrationImport, bridgeMonitorCommand{
		Version: "monitor.v1", ID: "bridge-import-component-1", Kind: "upsert_component",
		AtUnix: now.Unix(), ImportMode: true,
		Component: &bridgeMonitorComponent{
			ID: "bridge-imported", Name: "Imported Component", Kind: "service",
			FallbackStatus: "operational", Launched: true, LaunchedSet: true,
		},
	}, &migrationResult)
	if migrationResult.CommandID != "bridge-import-component-1" {
		t.Fatalf("monitor migration result = %#v", migrationResult)
	}

	subscribe := bridgeSubscribeRequest{
		Version:             subscriberContractVersion,
		RequestID:           "bridge-subscribe-1",
		Email:               "bridge-owner@example.test",
		ConfirmationToken:   "bridge-confirm-1",
		UnsubscribeToken:    "bridge-unsubscribe-1",
		ConfirmationSubject: "Confirm bridge subscription",
		ConfirmationBody:    "confirm through bridge",
		RequestedAt:         now,
	}
	var subscribed subscriberCommandResult
	postBridgeEvent(t, server.URL, "bridge-test-token", eventSubscriberSubscribe, subscribe, &subscribed)
	if subscribed.IntentCount != 1 {
		t.Fatalf("subscribe result = %#v", subscribed)
	}

	var claim outboxClaim
	postBridgeEvent(t, server.URL, "bridge-test-token", eventEmailOutboxClaim, outboxClaimRequest{
		Version: subscriberContractVersion, WorkerID: "bridge-test-worker", Limit: 10,
	}, &claim)
	if len(claim.Intents) != 1 || claim.Intents[0].Recipient != subscribe.Email {
		t.Fatalf("outbox claim = %#v", claim)
	}
	var receipt receiptApplyResult
	postBridgeEvent(t, server.URL, "bridge-test-token", eventEmailReceiptApply, outboxReceipt{
		Version: subscriberContractVersion, IntentID: claim.Intents[0].IntentID, Status: "delivered",
	}, &receipt)
	if !receipt.Applied {
		t.Fatalf("receipt result = %#v", receipt)
	}

	confirmedAt := now
	var importedSubscribers struct {
		Imported int `json:"imported"`
	}
	postBridgeEvent(t, server.URL, "bridge-test-token", eventSubscriberMigrationImport, bridgeSubscriberMigrationImportRequest{
		Version: subscriberContractVersion, RequestID: "bridge-subscriber-import-1",
		Subscribers: []bridgeLegacySubscriberRow{{
			ID: "legacy-subscriber-1", Email: "legacy@example.test", ConfirmedAt: &confirmedAt, CreatedAt: now,
		}},
	}, &importedSubscribers)
	if importedSubscribers.Imported != 1 {
		t.Fatalf("subscriber migration result = %#v", importedSubscribers)
	}
	var listedSubscribers struct {
		Subscribers []struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"subscribers"`
	}
	postBridgeEvent(t, server.URL, "bridge-test-token", eventSubscriberAdminList, bridgeSubscriberAdminListRequest{
		Version: subscriberContractVersion,
	}, &listedSubscribers)
	foundLegacySubscriber := false
	for _, subscriber := range listedSubscribers.Subscribers {
		if subscriber.ID == "legacy-subscriber-1" && subscriber.Email == "legacy@example.test" {
			foundLegacySubscriber = true
		}
	}
	if !foundLegacySubscriber {
		t.Fatalf("admin subscriber list omitted migrated row: %#v", listedSubscribers)
	}

	var sourceRevoked sourceLifecycleResult
	postBridgeEvent(t, server.URL, "bridge-test-token", eventHostSourceAdminRevoke, sourceAdminRevokeRequest{
		Version: sourceLifecycleContractVersion, RequestID: "bridge-source-revoke-1",
		SourceID: "bridge-vendor", CredentialID: "bridge-vendor-credential",
		ActorID: "bridge-admin", RevokedAt: now.Add(2 * time.Minute),
	}, &sourceRevoked)
	if !sourceRevoked.Completed {
		t.Fatalf("source lifecycle revoke = %#v", sourceRevoked)
	}
	postBridgeEvent(t, server.URL, "bridge-test-token", eventHostAuthSourceValidate, bridgeAuthSourceCredentialValidateRequest{
		Version: "bananapulse.auth/v1", RequestID: "bridge-source-validate-2",
		Token: "bridge-vendor-test-token", ValidatedAt: now.Add(3 * time.Minute),
	}, &sourceValidated)
	if sourceValidated.Valid {
		t.Fatalf("revoked source credential remained valid: %#v", sourceValidated)
	}

	var projection map[string]any
	postBridgeEvent(t, server.URL, "bridge-test-token", eventMonitorProjection, map[string]any{}, &projection)
	components, ok := projection["components"].([]any)
	if !ok || len(components) != 2 {
		t.Fatalf("monitor projection = %#v", projection)
	}
	var projected map[string]any
	for _, raw := range components {
		candidate, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		metadata, _ := candidate["component"].(map[string]any)
		if metadata["id"] == "bridge-database" {
			projected = candidate
			break
		}
	}
	if projected == nil || projected["own_evaluation"] == nil {
		t.Fatalf("monitor projection lost database evaluation: %#v", components)
	}
	metadata, ok := projected["component"].(map[string]any)
	if !ok || metadata["tag"] != "database" || fmt.Sprint(metadata["sort_order"]) != "10" {
		t.Fatalf("monitor projection lost application metadata: %#v", projected["component"])
	}
}

func TestHTTPBridgeCallsAuthOwnerDirectly(t *testing.T) {
	observer := &providerObserver{ready: make(chan run.ApplicationProviderAccess, 1)}
	appPath := filepath.Clean(filepath.Join("..", "application", "pulp.app.toml"))
	runtime, err := run.NewDirectApplicationRuntime(appPath, run.DirectApplicationOptions{
		StorageRoot: t.TempDir(),
		Lifecycle:   observer,
	})
	if err != nil {
		t.Fatalf("create Bananapulse runtime: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runtime.Start(ctx); err != nil {
		t.Fatalf("start Bananapulse runtime: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := runtime.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown Bananapulse runtime: %v", err)
		}
	})
	client, err := newApplicationClient(<-observer.ready)
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := newHTTPBridgeWithFamilies(client, "bridge-test-token", bridgeFamilies{
		auth: true, authAdmin: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(bridge.Handler())
	t.Cleanup(server.Close)

	now := time.Date(2026, 7, 26, 1, 30, 0, 0, time.UTC)
	var imported struct {
		IdentityID string `json:"identity_id"`
		Imported   bool   `json:"imported"`
	}
	postBridgeEvent(t, server.URL, "bridge-test-token", eventHostAuthAdminIdentityImport, bridgeAuthAdminIdentityImportRequest{
		Version: "bananapulse.auth/v1", RequestID: "auth-import-1", IdentityID: "admin-1",
		Email: "admin@example.test", State: "enabled", ImportedAt: now,
	}, &imported)
	if !imported.Imported || imported.IdentityID != "admin-1" {
		t.Fatalf("identity import = %#v", imported)
	}

	var issued struct {
		Accepted    bool   `json:"accepted"`
		Deliver     bool   `json:"deliver"`
		ChallengeID string `json:"challenge_id"`
	}
	postBridgeEvent(t, server.URL, "bridge-test-token", eventHostAuthMagicLinkIssue, bridgeAuthMagicLinkIssueRequest{
		Version: "bananapulse.auth/v1", RequestID: "auth-issue-1", Email: "admin@example.test",
		Token: "test-magic-token", IssuedAt: now, ExpiresAt: now.Add(15 * time.Minute),
	}, &issued)
	if !issued.Accepted || !issued.Deliver || issued.ChallengeID == "" {
		t.Fatalf("magic-link issue = %#v", issued)
	}

	var consumed struct {
		Authenticated bool   `json:"authenticated"`
		ChallengeID   string `json:"challenge_id"`
		IdentityID    string `json:"identity_id"`
	}
	postBridgeEvent(t, server.URL, "bridge-test-token", eventHostAuthMagicLinkConsume, bridgeAuthMagicLinkConsumeRequest{
		Version: "bananapulse.auth/v1", RequestID: "auth-consume-1",
		Token: "test-magic-token", ConsumedAt: now.Add(time.Minute),
	}, &consumed)
	if !consumed.Authenticated || consumed.ChallengeID != issued.ChallengeID || consumed.IdentityID != "admin-1" {
		t.Fatalf("magic-link consume = %#v", consumed)
	}

	var created struct {
		SessionID string `json:"session_id"`
		Created   bool   `json:"created"`
	}
	postBridgeEvent(t, server.URL, "bridge-test-token", eventHostAuthSessionCreate, bridgeAuthSessionCreateRequest{
		Version: "bananapulse.auth/v1", RequestID: "auth-session-1",
		ChallengeID: consumed.ChallengeID, IdentityID: consumed.IdentityID, Token: "test-session-token",
		IssuedAt: now.Add(time.Minute), ExpiresAt: now.Add(24 * time.Hour),
	}, &created)
	if !created.Created || created.SessionID == "" {
		t.Fatalf("session create = %#v", created)
	}

	var validated struct {
		Valid      bool   `json:"valid"`
		SessionID  string `json:"session_id"`
		IdentityID string `json:"identity_id"`
		Role       string `json:"role"`
	}
	postBridgeEvent(t, server.URL, "bridge-test-token", eventHostAuthSessionValidate, bridgeAuthSessionValidateRequest{
		Version: "bananapulse.auth/v1", Token: "test-session-token", At: now.Add(2 * time.Minute),
	}, &validated)
	if !validated.Valid || validated.SessionID != created.SessionID ||
		validated.IdentityID != "admin-1" || validated.Role != "admin" {
		t.Fatalf("session validate = %#v", validated)
	}
}

func TestHTTPBridgeHealthAuthorizationAndAllowlist(t *testing.T) {
	bridge := &httpBridge{token: "bridge-test-token"}
	server := httptest.NewServer(bridge.Handler())
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", response.StatusCode)
	}
	response.Body.Close()

	request, err := http.NewRequest(http.MethodPost, server.URL+bridgeEventsPath+eventMonitorProjection, bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatal(err)
	}
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", response.StatusCode)
	}
	response.Body.Close()

	request, err = http.NewRequest(http.MethodPost, server.URL+bridgeEventsPath+"not-allowed", bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(bridgeTokenHeader, "bridge-test-token")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("allowlist status = %d", response.StatusCode)
	}
	response.Body.Close()
}

func TestHTTPBridgeSecondWaveFamiliesFailClosed(t *testing.T) {
	bridge := &httpBridge{token: "bridge-test-token"}
	server := httptest.NewServer(bridge.Handler())
	t.Cleanup(server.Close)

	for _, test := range []struct {
		event string
		body  string
	}{
		{event: eventMonitorCommand, body: `{"version":"monitor.v1","id":"admin-1","kind":"upsert_component","at_unix":1}`},
		{event: eventSubscriberAdminList, body: `{"version":"bananapulse.subscribers/v1"}`},
		{event: eventSubscriberMigrationImport, body: `{"version":"bananapulse.subscribers/v1","request_id":"import-1","subscribers":[]}`},
		{event: eventHostAuthSessionValidate, body: `{"version":"bananapulse.auth/v1","token":"opaque","at":"2026-07-26T00:00:00Z"}`},
	} {
		request, err := http.NewRequest(http.MethodPost, server.URL+bridgeEventsPath+test.event, bytes.NewBufferString(test.body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set(bridgeTokenHeader, "bridge-test-token")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want disabled family rejection", test.event, response.StatusCode)
		}
	}
}

func TestAuthBridgeUsesExactTypedContracts(t *testing.T) {
	for _, event := range []string{
		eventHostAuthAdminIdentityImport,
		eventHostAuthMagicLinkIssue,
		eventHostAuthMagicLinkConsume,
		eventHostAuthSessionCreate,
		eventHostAuthSessionValidate,
		eventHostAuthSessionRevoke,
		eventHostAuthAPITokenIssue,
		eventHostAuthAPITokenValidate,
		eventHostAuthAPITokenAdminImport,
		eventHostAuthAPITokenAdminList,
		eventHostAuthAPITokenAdminRevoke,
		eventHostAuthSourceAdminImport,
		eventHostAuthSourceAdminRotate,
		eventHostAuthSourceAdminRevoke,
		eventHostAuthSourceValidate,
		eventHostAuthProjection,
		eventHostAuthAdminAuditQuery,
	} {
		provider, request, ok := authBridgeRequest(event)
		if !ok || provider == "" || request == nil {
			t.Fatalf("auth event %q has no exact provider/request binding", event)
		}
	}
	if _, _, ok := authBridgeRequest("bananapulse.host.auth.not-real.v1"); ok {
		t.Fatal("invented auth event was accepted")
	}

	body := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(
		`{"version":"bananapulse.auth/v1","token":"opaque","at":"2026-07-26T00:00:00Z","unexpected":true}`,
	))
	_, target, _ := authBridgeRequest(eventHostAuthSessionValidate)
	if err := decodeBridgeJSON(body, target, false); err == nil {
		t.Fatal("auth request accepted an unknown field")
	}
}

func TestMonitorBridgeDomainErrorsAreSanitizedAndTyped(t *testing.T) {
	for _, test := range []struct {
		message string
		status  int
		code    string
	}{
		{message: `source target "private-label" is not mapped`, status: http.StatusUnprocessableEntity, code: "unmapped_target"},
		{message: `source "missing-source" not found`, status: http.StatusNotFound, code: "not_found"},
		{message: `cannot archive component "api" with a live declared outage`, status: http.StatusConflict, code: "conflict"},
		{message: `invalid observation signal "maybe"`, status: http.StatusBadRequest, code: "invalid_request"},
	} {
		domain, ok := classifyBridgeDomainError(&bridgeDispatchError{
			event: eventMonitorIngestAuthenticated,
			err:   errors.New(test.message),
		})
		if !ok || domain.Status != test.status || domain.Code != test.code {
			t.Fatalf("classify %q = %#v, %v", test.message, domain, ok)
		}
		if strings.Contains(domain.Message, "private-label") || strings.Contains(domain.Message, "missing-source") {
			t.Fatalf("domain error leaked owner detail: %#v", domain)
		}
	}
	if _, ok := classifyBridgeDomainError(&bridgeDispatchError{
		event: eventHostAuthSessionValidate,
		err:   errors.New(`token "secret" not found`),
	}); ok {
		t.Fatal("auth error was exposed through monitor domain mapping")
	}
}

func TestBridgeRequiresAuthenticationBeyondLoopback(t *testing.T) {
	for _, test := range []struct {
		address string
		want    bool
	}{
		{address: "127.0.0.1:8788", want: true},
		{address: "[::1]:8788", want: true},
		{address: "localhost:8788", want: true},
		{address: "0.0.0.0:8788", want: false},
		{address: ":8788", want: false},
		{address: "pulp-host:8788", want: false},
	} {
		if got := bridgeAddressIsLoopback(test.address); got != test.want {
			t.Fatalf("bridgeAddressIsLoopback(%q) = %v, want %v", test.address, got, test.want)
		}
	}
}

func postBridgeEvent(t *testing.T, baseURL, token, event string, requestValue, responseValue any) {
	t.Helper()
	body, err := json.Marshal(requestValue)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+bridgeEventsPath+event, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(bridgeTokenHeader, token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("POST %s status = %d", event, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(responseValue); err != nil {
		t.Fatalf("decode %s response: %v", event, err)
	}
}
