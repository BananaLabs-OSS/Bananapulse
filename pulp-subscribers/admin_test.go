package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

func TestAdminProjectionIsSeparateFromPublicPrivacyProjection(t *testing.T) {
	store, owner := openTestOwner(t, filepath.Join(t.TempDir(), "subscribers.sqlite"))
	defer store.Close()
	created := time.Date(2026, 7, 25, 3, 4, 5, 0, time.UTC)
	result, err := owner.Subscribe(context.Background(), SubscribeRequest{
		Version:           ContractVersion,
		RequestID:         "subscribe-admin-1",
		Email:             "Admin-Visible@Example.Test",
		ConfirmationToken: "confirmation-private",
		UnsubscribeToken:  "unsubscribe-private",
		RequestedAt:       created,
	})
	if err != nil {
		t.Fatal(err)
	}

	public, err := owner.Projection(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wire, err := msgpack.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"admin-visible@example.test", "confirmation-private", "unsubscribe-private", result.SubscriberID} {
		if strings.Contains(string(wire), private) {
			t.Fatalf("public projection exposed %q", private)
		}
	}

	list, err := owner.AdminList(context.Background(), AdminListRequest{Version: ContractVersion})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Subscribers) != 1 {
		t.Fatalf("admin list = %#v", list)
	}
	subscriber := list.Subscribers[0]
	if subscriber.ID != result.SubscriberID || subscriber.Email != "admin-visible@example.test" ||
		subscriber.State != "pending" || !subscriber.CreatedAt.Equal(created) || subscriber.ConfirmedAt != nil {
		t.Fatalf("admin subscriber = %#v", subscriber)
	}
	get, err := owner.AdminGet(context.Background(), AdminGetRequest{Version: ContractVersion, SubscriberID: result.SubscriberID})
	if err != nil {
		t.Fatal(err)
	}
	if !get.Found || get.Subscriber == nil || get.Subscriber.Email != subscriber.Email {
		t.Fatalf("admin get = %#v", get)
	}
}

func TestAdminStateAndDeleteReceiptsSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscribers.sqlite")
	store, owner := openTestOwner(t, path)
	subscriber := subscribe(t, owner, "subscribe-admin-state", "state@example.test", "confirm-state", "unsubscribe-state")
	changedAt := time.Date(2026, 7, 25, 6, 7, 8, 0, time.UTC)
	changed, err := owner.AdminStateSet(context.Background(), AdminStateSetRequest{
		Version: ContractVersion, RequestID: "admin-state-1", SubscriberID: subscriber.SubscriberID,
		State: "confirmed", ChangedAt: changedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed.Found || !changed.Changed || changed.State != "confirmed" {
		t.Fatalf("state result = %#v", changed)
	}
	get, err := owner.AdminGet(context.Background(), AdminGetRequest{Version: ContractVersion, SubscriberID: subscriber.SubscriberID})
	if err != nil {
		t.Fatal(err)
	}
	if get.Subscriber == nil || get.Subscriber.ConfirmedAt == nil || !get.Subscriber.ConfirmedAt.Equal(changedAt) {
		t.Fatalf("confirmed subscriber = %#v", get)
	}
	deleted, err := owner.AdminDelete(context.Background(), AdminDeleteRequest{
		Version: ContractVersion, RequestID: "admin-delete-1", SubscriberID: subscriber.SubscriberID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !deleted.Found || !deleted.Changed {
		t.Fatalf("delete result = %#v", deleted)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, owner = openTestOwner(t, path)
	defer store.Close()
	replayed, err := owner.AdminDelete(context.Background(), AdminDeleteRequest{
		Version: ContractVersion, RequestID: "admin-delete-1", SubscriberID: subscriber.SubscriberID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayed != deleted {
		t.Fatalf("delete replay = %#v, want %#v", replayed, deleted)
	}
	if _, err := owner.AdminDelete(context.Background(), AdminDeleteRequest{
		Version: ContractVersion, RequestID: "admin-delete-1", SubscriberID: "different-id",
	}); err == nil || !strings.Contains(err.Error(), "different command") {
		t.Fatalf("changed delete replay error = %v", err)
	}
	get, err = owner.AdminGet(context.Background(), AdminGetRequest{Version: ContractVersion, SubscriberID: subscriber.SubscriberID})
	if err != nil {
		t.Fatal(err)
	}
	if get.Found {
		t.Fatalf("deleted subscriber remained: %#v", get)
	}
}

func TestLegacyImportIsAtomicIdempotentAndPreservesOutstandingLinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscribers.sqlite")
	store, owner := openTestOwner(t, path)
	createdPending := time.Date(2026, 7, 20, 1, 0, 0, 0, time.UTC)
	createdConfirmed := time.Date(2026, 7, 21, 2, 0, 0, 0, time.UTC)
	confirmedAt := time.Date(2026, 7, 21, 3, 0, 0, 0, time.UTC)
	request := MigrationImportRequest{
		Version: ContractVersion, RequestID: "legacy-import-1",
		Subscribers: []LegacySubscriberRow{
			{ID: "legacy-pending", Email: "Pending@Example.Test", CreatedAt: createdPending},
			{ID: "legacy-confirmed", Email: "confirmed@example.test", CreatedAt: createdConfirmed, ConfirmedAt: &confirmedAt},
		},
	}
	first, err := owner.ImportLegacy(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Imported != 2 || first.Unchanged != 0 {
		t.Fatalf("first receipt = %#v", first)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, owner = openTestOwner(t, path)
	defer store.Close()
	replayed, err := owner.ImportLegacy(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replayed != first {
		t.Fatalf("replayed receipt = %#v, want %#v", replayed, first)
	}
	reimport := request
	reimport.RequestID = "legacy-import-2"
	unchanged, err := owner.ImportLegacy(context.Background(), reimport)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Imported != 0 || unchanged.Unchanged != 2 {
		t.Fatalf("second import receipt = %#v", unchanged)
	}
	changed := request
	changed.Subscribers = append([]LegacySubscriberRow(nil), request.Subscribers...)
	changed.Subscribers[0].Email = "changed@example.test"
	if _, err := owner.ImportLegacy(context.Background(), changed); err == nil ||
		!strings.Contains(err.Error(), "different payload") {
		t.Fatalf("changed replay error = %v", err)
	}

	confirmed, err := owner.Confirm(context.Background(), ConfirmRequest{
		Version: ContractVersion, RequestID: "confirm-legacy", Token: "legacy-pending",
	})
	if err != nil || !confirmed.Confirmed {
		t.Fatalf("legacy confirmation = %#v, %v", confirmed, err)
	}
	unsubscribed, err := owner.Unsubscribe(context.Background(), UnsubscribeRequest{
		Version: ContractVersion, RequestID: "unsubscribe-legacy", Token: "legacy-confirmed",
	})
	if err != nil || !unsubscribed.Unsubscribed {
		t.Fatalf("legacy unsubscribe = %#v, %v", unsubscribed, err)
	}
}

func TestLegacyImportRollsBackWholeBatchOnConflict(t *testing.T) {
	store, owner := openTestOwner(t, filepath.Join(t.TempDir(), "subscribers.sqlite"))
	defer store.Close()
	subscribe(t, owner, "existing-subscription", "existing@example.test", "existing-confirm", "existing-unsubscribe")
	createdAt := time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC)
	_, err := owner.ImportLegacy(context.Background(), MigrationImportRequest{
		Version: ContractVersion, RequestID: "conflicting-import",
		Subscribers: []LegacySubscriberRow{
			{ID: "would-have-imported", Email: "new@example.test", CreatedAt: createdAt},
			{ID: "different-id", Email: "existing@example.test", CreatedAt: createdAt},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts with owner state") {
		t.Fatalf("conflicting import error = %v", err)
	}
	get, err := owner.AdminGet(context.Background(), AdminGetRequest{
		Version: ContractVersion, SubscriberID: "would-have-imported",
	})
	if err != nil {
		t.Fatal(err)
	}
	if get.Found {
		t.Fatal("partial legacy batch survived rollback")
	}
}

func TestConfirmationResendDoesNotLeakStateAndIsIdempotentAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscribers.sqlite")
	store, owner := openTestOwner(t, path)
	subscribe(t, owner, "subscribe-resend", "person@example.test", "confirmation-resend", "unsubscribe-resend")
	request := ConfirmationResendRequest{
		Version: ContractVersion, RequestID: "resend-1", Email: "person@example.test",
		ConfirmationToken: "confirmation-resend", ConfirmationSubject: "Confirm again",
		ConfirmationBody: "private confirmation link", RequestedAt: time.Date(2026, 7, 25, 8, 0, 0, 0, time.UTC),
	}
	found, err := owner.ResendConfirmation(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	unknown := request
	unknown.RequestID = "resend-unknown"
	unknown.Email = "unknown@example.test"
	missing, err := owner.ResendConfirmation(context.Background(), unknown)
	if err != nil {
		t.Fatal(err)
	}
	if found != missing || !found.Accepted {
		t.Fatalf("resend leaked state: found=%#v missing=%#v", found, missing)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, owner = openTestOwner(t, path)
	defer store.Close()
	replayed, err := owner.ResendConfirmation(context.Background(), request)
	if err != nil || replayed != found {
		t.Fatalf("resend replay = %#v, %v", replayed, err)
	}
	claim, err := owner.ClaimOutbox(context.Background(), OutboxClaimRequest{Version: ContractVersion, WorkerID: "email-host", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	resendCount := 0
	for _, intent := range claim.Intents {
		if intent.Subject == "Confirm again" {
			resendCount++
		}
	}
	if resendCount != 1 {
		t.Fatalf("resend intents = %d, want 1", resendCount)
	}
}

func TestNotificationDeliveryBodyGetsPrivatePerRecipientUnsubscribeLink(t *testing.T) {
	store, owner := openTestOwner(t, filepath.Join(t.TempDir(), "subscribers.sqlite"))
	defer store.Close()
	subscribe(t, owner, "subscribe-private-link", "person@example.test", "confirmation-link", "unsubscribe-link")
	confirm(t, owner, "confirm-private-link", "confirmation-link")
	_, err := owner.NotifyIncident(context.Background(), NotificationRequest{
		Version: ContractVersion, RequestID: "notify-private-link", EventID: "incident-private-link",
		Subject: "Incident", Body: "Details\n{{unsubscribe_url}}",
		UnsubscribeBaseURL: "https://status.example.test/api/unsubscribe", OccurredAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	public, err := owner.Projection(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wire, err := msgpack.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "unsubscribe-link") {
		t.Fatal("public projection exposed unsubscribe token")
	}
	claim, err := owner.ClaimOutbox(context.Background(), OutboxClaimRequest{Version: ContractVersion, WorkerID: "email-host", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var body string
	for _, intent := range claim.Intents {
		if intent.Kind == "status.incident" {
			body = intent.Body
		}
	}
	if body != "Details\nhttps://status.example.test/api/unsubscribe?token=unsubscribe-link" {
		t.Fatalf("private delivery body = %q", body)
	}
}
