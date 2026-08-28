package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

func setDeliveryConfig(t *testing.T, owner *Owner, requestID, baseURL string) DeliveryConfigSetResult {
	t.Helper()
	result, err := owner.SetDeliveryConfig(context.Background(), DeliveryConfigSetRequest{
		Version: ContractVersion, RequestID: requestID, UnsubscribeBaseURL: baseURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func confirmedSubscriber(t *testing.T, owner *Owner, suffix string) {
	t.Helper()
	subscribe(t, owner, "subscribe-"+suffix, suffix+"@example.test", "confirm-"+suffix, "unsubscribe-"+suffix)
	confirm(t, owner, "confirm-request-"+suffix, "confirm-"+suffix)
}

func incidentTransitionResult(commandID string, deduped bool) MonitorCommandResult {
	return MonitorCommandResult{
		Version: monitorContractVersion, CommandID: commandID, Revision: 12, Deduped: deduped,
		Transitions: []DomainTransition{{
			ID: commandID + "/incident.opened/incident-1", Kind: "incident.opened", EntityID: "incident-1",
			ComponentID: "api", AffectedComponentIDs: []string{"api", "checkout"},
			Status: "investigating", Severity: "major", AtUnix: 1785000000,
			Incident: &TransitionIncident{
				ID: "incident-1", Title: "Checkout latency", Summary: "Payments are taking longer than normal.",
				Status: "investigating", Severity: "major", Affects: []string{"api", "checkout"},
				StartedAtUnix: 1785000000, CreatedAtUnix: 1785000000,
			},
		}},
	}
}

func TestTransitionApplyConsumesFirstDedupedMonitorRetryExactlyOnceAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscribers.sqlite")
	store, owner := openTestOwner(t, path)
	confirmedSubscriber(t, owner, "one")
	confirmedSubscriber(t, owner, "two")
	setDeliveryConfig(t, owner, "delivery-config-1", "https://status.example.test/api/unsubscribe")

	// The monitor committed successfully, but a hypothetical first subscriber
	// call was lost. Its retry is marked deduped while retaining transitions.
	retry := incidentTransitionResult("monitor-command-1", true)
	rawMap := map[string]any{
		"version": retry.Version, "command_id": retry.CommandID, "revision": retry.Revision,
		"deduped": retry.Deduped, "component_ids": []string{"api"}, "unknown_future_field": "ignored",
		"transitions": retry.Transitions,
	}
	raw, err := msgpack.Marshal(rawMap)
	if err != nil {
		t.Fatal(err)
	}
	first, err := owner.ApplyTransitionRaw(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if first.Suppressed || first.TransitionCount != 1 || first.IntentCount != 2 {
		t.Fatalf("first deduped retry apply = %#v", first)
	}
	second, err := owner.ApplyTransition(context.Background(), retry)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Suppressed || second.IntentCount != 0 {
		t.Fatalf("subscriber replay = %#v", second)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, owner = openTestOwner(t, path)
	defer store.Close()
	third, err := owner.ApplyTransition(context.Background(), retry)
	if err != nil {
		t.Fatal(err)
	}
	if !third.Suppressed || third.IntentCount != 0 {
		t.Fatalf("subscriber replay after restart = %#v", third)
	}
	claim, err := owner.ClaimOutbox(context.Background(), OutboxClaimRequest{
		Version: ContractVersion, WorkerID: "email-host", Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	var transitionIntents []OutboxIntent
	for _, intent := range claim.Intents {
		if intent.Kind == "status.incident.opened" {
			transitionIntents = append(transitionIntents, intent)
		}
	}
	if len(transitionIntents) != 2 {
		t.Fatalf("transition intents = %d, want 2", len(transitionIntents))
	}
	for _, intent := range transitionIntents {
		if intent.Subject != "Incident opened: Checkout latency" ||
			!strings.Contains(intent.Body, "Payments are taking longer than normal.") ||
			!strings.Contains(intent.Body, "https://status.example.test/api/unsubscribe?token=") {
			t.Fatalf("transition intent = %#v", intent)
		}
		if intent.Recipient == "one@example.test" && !strings.Contains(intent.Body, "unsubscribe-one") {
			t.Fatalf("recipient one received wrong private link: %q", intent.Body)
		}
		if intent.Recipient == "two@example.test" && !strings.Contains(intent.Body, "unsubscribe-two") {
			t.Fatalf("recipient two received wrong private link: %q", intent.Body)
		}
	}
}

func TestTransitionApplyFailsClosedUntilDurableDeliveryConfigExists(t *testing.T) {
	store, owner := openTestOwner(t, filepath.Join(t.TempDir(), "subscribers.sqlite"))
	defer store.Close()
	confirmedSubscriber(t, owner, "config")
	result := incidentTransitionResult("monitor-command-config", false)
	if _, err := owner.ApplyTransition(context.Background(), result); err == nil ||
		!strings.Contains(err.Error(), "delivery config is required") {
		t.Fatalf("missing config error = %v", err)
	}
	setDeliveryConfig(t, owner, "delivery-config-present", "https://status.example.test/api/unsubscribe")
	applied, err := owner.ApplyTransition(context.Background(), result)
	if err != nil {
		t.Fatal(err)
	}
	if applied.IntentCount != 1 {
		t.Fatalf("applied after config = %#v", applied)
	}
}

func TestDeliveryConfigIsValidatedPayloadFencedAndRestartSafe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscribers.sqlite")
	store, owner := openTestOwner(t, path)
	for _, invalid := range []string{
		"/api/unsubscribe",
		"https://user@example.test/api/unsubscribe",
		"https://status.example.test/api/unsubscribe#fragment",
		"https://status.example.test/api/unsubscribe?token=preloaded",
	} {
		if _, err := owner.SetDeliveryConfig(context.Background(), DeliveryConfigSetRequest{
			Version: ContractVersion, RequestID: "invalid-" + secretHash(invalid), UnsubscribeBaseURL: invalid,
		}); err == nil {
			t.Fatalf("invalid delivery config accepted: %q", invalid)
		}
	}
	first := setDeliveryConfig(t, owner, "delivery-config-restart", "https://status.example.test/api/unsubscribe")
	if !first.Applied {
		t.Fatalf("first config result = %#v", first)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, owner = openTestOwner(t, path)
	defer store.Close()
	replayed := setDeliveryConfig(t, owner, "delivery-config-restart", "https://status.example.test/api/unsubscribe")
	if replayed != first {
		t.Fatalf("config replay = %#v, want %#v", replayed, first)
	}
	if _, err := owner.SetDeliveryConfig(context.Background(), DeliveryConfigSetRequest{
		Version: ContractVersion, RequestID: "delivery-config-restart",
		UnsubscribeBaseURL: "https://other.example.test/api/unsubscribe",
	}); err == nil || !strings.Contains(err.Error(), "different command") {
		t.Fatalf("changed config replay error = %v", err)
	}
}

func TestTransitionApplySuppressesEmptyImportResultWithoutConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscribers.sqlite")
	store, owner := openTestOwner(t, path)
	empty := MonitorCommandResult{
		Version: monitorContractVersion, CommandID: "monitor-import-1", Revision: 7,
		Transitions: nil,
	}
	first, err := owner.ApplyTransition(context.Background(), empty)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Suppressed || first.IntentCount != 0 || first.TransitionCount != 0 {
		t.Fatalf("empty import result = %#v", first)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, owner = openTestOwner(t, path)
	defer store.Close()
	replayed, err := owner.ApplyTransition(context.Background(), empty)
	if err != nil || replayed.IntentCount != 0 || !replayed.Suppressed {
		t.Fatalf("empty replay = %#v, %v", replayed, err)
	}
}

func TestTransitionApplyRejectsChangedReceiptAndMissingPrivateToken(t *testing.T) {
	store, owner := openTestOwner(t, filepath.Join(t.TempDir(), "subscribers.sqlite"))
	defer store.Close()
	setDeliveryConfig(t, owner, "delivery-config-fenced", "https://status.example.test/api/unsubscribe")
	result := incidentTransitionResult("monitor-command-fenced", false)
	first, err := owner.ApplyTransition(context.Background(), MonitorCommandResult{
		Version: monitorContractVersion, CommandID: result.CommandID, Revision: result.Revision,
	})
	if err != nil || !first.Suppressed {
		t.Fatalf("initial empty receipt = %#v, %v", first, err)
	}
	if _, err := owner.ApplyTransition(context.Background(), result); err == nil ||
		!strings.Contains(err.Error(), "different result") {
		t.Fatalf("changed monitor receipt error = %v", err)
	}

	// Simulate an owner row created before private delivery tokens existed.
	if _, err := owner.store.db.ExecContext(context.Background(),
		`INSERT INTO subscribers(id,email,confirmation_hash,unsubscribe_hash,state,created_at,confirmed_at) VALUES (?,?,?,?,?,?,?)`,
		"old-confirmed", "old@example.test", secretHash("old-confirm"), secretHash("old-unsubscribe"),
		"confirmed", time.Now().UTC().UnixNano(), sql.NullInt64{Int64: time.Now().UTC().UnixNano(), Valid: true},
	); err != nil {
		t.Fatal(err)
	}
	missingToken := incidentTransitionResult("monitor-command-old-token", false)
	if _, err := owner.ApplyTransition(context.Background(), missingToken); err == nil ||
		!strings.Contains(err.Error(), "no private unsubscribe token") {
		t.Fatalf("missing private token error = %v", err)
	}
}

func TestTransitionApplyRequiresStableMonitorTransitionID(t *testing.T) {
	store, owner := openTestOwner(t, filepath.Join(t.TempDir(), "subscribers.sqlite"))
	defer store.Close()
	setDeliveryConfig(t, owner, "delivery-config-id", "https://status.example.test/api/unsubscribe")
	result := incidentTransitionResult("monitor-command-id", false)
	result.Transitions[0].ID = "wrong"
	if _, err := owner.ApplyTransition(context.Background(), result); err == nil ||
		!strings.Contains(err.Error(), "does not match") {
		t.Fatalf("unstable transition id error = %v", err)
	}
}
