package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

func openTestOwner(t *testing.T, path string) (*Store, *Owner) {
	t.Helper()
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := OpenOwner(store)
	if err != nil {
		t.Fatal(err)
	}
	return store, owner
}
func subscribe(t *testing.T, owner *Owner, requestID, email, confirmation, unsubscribe string) CommandResult {
	t.Helper()
	result, err := owner.Subscribe(context.Background(), SubscribeRequest{Version: ContractVersion, RequestID: requestID, Email: email, ConfirmationToken: confirmation, UnsubscribeToken: unsubscribe, RequestedAt: time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func confirm(t *testing.T, owner *Owner, requestID, token string) {
	t.Helper()
	if _, err := owner.Confirm(context.Background(), ConfirmRequest{Version: ContractVersion, RequestID: requestID, Token: token}); err != nil {
		t.Fatal(err)
	}
}

func TestSubscribeReplaySurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscribers.sqlite")
	store, owner := openTestOwner(t, path)
	first := subscribe(t, owner, "subscribe-1", "person@example.test", "confirm-1", "unsubscribe-1")
	if !first.Created {
		t.Fatal("first subscribe did not create")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, owner = openTestOwner(t, path)
	deferred := subscribe(t, owner, "subscribe-1", "different@example.test", "other-confirm", "other-unsubscribe")
	if deferred != first {
		t.Fatalf("replay = %#v, want %#v", deferred, first)
	}
	confirm(t, owner, "confirm-1", "confirm-1")
	p, err := owner.Projection(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.ConfirmedCount != 1 || p.PendingCount != 0 {
		t.Fatalf("projection after restart = %#v", p)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSubscribePersistsApplicationPreparedConfirmationContentPrivately(t *testing.T) {
	store, owner := openTestOwner(t, filepath.Join(t.TempDir(), "subscribers.sqlite"))
	defer store.Close()
	_, err := owner.Subscribe(context.Background(), SubscribeRequest{
		Version:             ContractVersion,
		RequestID:           "subscribe-content-1",
		Email:               "person@example.test",
		ConfirmationToken:   "confirm-content-1",
		UnsubscribeToken:    "unsubscribe-content-1",
		ConfirmationSubject: "Confirm Example status updates",
		ConfirmationBody:    "https://status.example.test/api/subscribe/confirm?token=confirm-content-1",
		RequestedAt:         time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := owner.ClaimOutbox(context.Background(), OutboxClaimRequest{
		Version: ContractVersion, WorkerID: "email-host", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(claim.Intents) != 1 {
		t.Fatalf("confirmation intents = %d, want 1", len(claim.Intents))
	}
	if claim.Intents[0].Subject != "Confirm Example status updates" ||
		claim.Intents[0].Body != "https://status.example.test/api/subscribe/confirm?token=confirm-content-1" {
		t.Fatalf("confirmation content = %#v", claim.Intents[0])
	}
}

func TestConfirmationUnsubscribeAndPrivacyProjection(t *testing.T) {
	store, owner := openTestOwner(t, filepath.Join(t.TempDir(), "subscribers.sqlite"))
	defer store.Close()
	subscribe(t, owner, "subscribe-1", "person@example.test", "confirm-1", "unsubscribe-1")
	confirm(t, owner, "confirm-1", "confirm-1")
	notified, err := owner.NotifyMaintenance(context.Background(), NotificationRequest{Version: ContractVersion, RequestID: "maintenance-request", EventID: "maintenance-1", Subject: "Scheduled maintenance", Body: "A window", OccurredAt: time.Now()})
	if err != nil || notified.IntentCount != 1 {
		t.Fatalf("notify = %#v, %v", notified, err)
	}
	if _, err := owner.Unsubscribe(context.Background(), UnsubscribeRequest{Version: ContractVersion, RequestID: "unsubscribe-1", Token: "unsubscribe-1"}); err != nil {
		t.Fatal(err)
	}
	notified, err = owner.NotifyIncident(context.Background(), NotificationRequest{Version: ContractVersion, RequestID: "incident-request", EventID: "incident-1", Subject: "Incident", Body: "Details", OccurredAt: time.Now()})
	if err != nil || notified.IntentCount != 0 {
		t.Fatalf("unsubscribed recipient notified: %#v, %v", notified, err)
	}
	p, err := owner.Projection(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wire, err := msgpack.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "person@example.test") || strings.Contains(string(wire), "confirm-1") || strings.Contains(string(wire), "unsubscribe-1") {
		t.Fatal("projection exposed a recipient")
	}
	if p.UnsubscribedCount != 1 || p.ConfirmedCount != 0 {
		t.Fatalf("projection = %#v", p)
	}
}

func TestNotificationIntentAndReceiptAreExactlyOnceAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscribers.sqlite")
	store, owner := openTestOwner(t, path)
	subscribe(t, owner, "subscribe-1", "person@example.test", "confirm-1", "unsubscribe-1")
	confirm(t, owner, "confirm-1", "confirm-1")
	request := NotificationRequest{Version: ContractVersion, RequestID: "incident-request-1", EventID: "incident-1", Subject: "Incident", Body: "Details", OccurredAt: time.Now()}
	first, err := owner.NotifyIncident(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.IntentCount != 1 {
		t.Fatalf("first intent count = %d", first.IntentCount)
	}
	request.RequestID = "incident-request-2"
	second, err := owner.NotifyIncident(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if second.IntentCount != 0 {
		t.Fatalf("duplicate event produced %d intents", second.IntentCount)
	}
	claim, err := owner.ClaimOutbox(context.Background(), OutboxClaimRequest{Version: ContractVersion, WorkerID: "email-host", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var incident OutboxIntent
	for _, intent := range claim.Intents {
		if intent.Kind == "status.incident" {
			incident = intent
		}
	}
	if incident.IntentID == "" {
		t.Fatal("incident intent missing")
	}
	firstReceipt, err := owner.ApplyReceipt(context.Background(), OutboxReceipt{Version: ContractVersion, IntentID: incident.IntentID, Status: ReceiptDelivered})
	if err != nil || !firstReceipt.Applied {
		t.Fatalf("first receipt = %#v, %v", firstReceipt, err)
	}
	secondReceipt, err := owner.ApplyReceipt(context.Background(), OutboxReceipt{Version: ContractVersion, IntentID: incident.IntentID, Status: ReceiptDelivered})
	if err != nil || secondReceipt.Applied {
		t.Fatalf("replayed receipt = %#v, %v", secondReceipt, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, owner = openTestOwner(t, path)
	defer store.Close()
	claim, err = owner.ClaimOutbox(context.Background(), OutboxClaimRequest{Version: ContractVersion, WorkerID: "email-host", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, intent := range claim.Intents {
		if intent.IntentID == incident.IntentID {
			t.Fatal("delivered intent was re-claimed after restart")
		}
	}
}
