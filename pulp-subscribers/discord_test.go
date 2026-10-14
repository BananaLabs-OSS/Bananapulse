package main

import (
	"context"
	"strings"
	"testing"
)

func TestDiscordTransitionsArePrivateDeduplicatedAndSeparatedFromEmail(t *testing.T) {
	ctx := context.Background()
	store, owner := openTestOwner(t, ":memory:")
	defer store.Close()
	setDeliveryConfig(t, owner, "discord-base", "https://status.example.test/unsubscribe")
	_, err := owner.SetDiscordConfig(ctx, DiscordConfig{Version: ContractVersion, Enabled: true, Outages: true, Recoveries: true})
	if err != nil {
		t.Fatal(err)
	}
	command := incidentTransitionResult("discord-open", false)
	command.Transitions[0].Incident.Summary = "Target 203.0.113.12:27016 timed out"
	if _, err = owner.ApplyTransition(ctx, command); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.ApplyTransition(ctx, command); err != nil {
		t.Fatal(err)
	}
	claim, err := owner.ClaimOutbox(ctx, OutboxClaimRequest{Version: ContractVersion, WorkerID: "discord", Limit: 25, Channel: "discord"})
	if err != nil {
		t.Fatal(err)
	}
	if len(claim.Intents) != 1 {
		t.Fatalf("intents: %#v", claim)
	}
	body := claim.Intents[0].Body
	if strings.Contains(body, "203.0.113.") || strings.Contains(body, "unsubscribe") || !strings.Contains(body, "https://status.example.test/incident/incident-1") {
		t.Fatalf("unsafe or incorrect payload: %s", body)
	}
	email, err := owner.ClaimOutbox(ctx, OutboxClaimRequest{Version: ContractVersion, WorkerID: "email", Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	if len(email.Intents) != 0 {
		t.Fatal("email worker received Discord payload")
	}
	// A resolved transition is independently queued and records duration.
	command.CommandID = "discord-resolved"
	tr := &command.Transitions[0]
	tr.Kind = "incident.resolved"
	tr.ID = command.CommandID + "/" + tr.Kind + "/" + tr.EntityID
	tr.Incident.ResolvedAtUnix = tr.Incident.StartedAtUnix + 18
	if _, err = owner.ApplyTransition(ctx, command); err != nil {
		t.Fatal(err)
	}
	claim, err = owner.ClaimOutbox(ctx, OutboxClaimRequest{Version: ContractVersion, WorkerID: "discord", Limit: 25, Channel: "discord"})
	if err != nil {
		t.Fatal(err)
	}
	if len(claim.Intents) != 2 {
		t.Fatalf("intents: %#v", claim)
	}
	found := false
	for _, i := range claim.Intents {
		if strings.Contains(i.Body, "Incident duration: 18s") {
			found = true
		}
	}
	if !found {
		t.Fatal("recovery missing duration")
	}
	_, err = owner.SetDiscordConfig(ctx, DiscordConfig{Version: ContractVersion, Enabled: false, Outages: true, Recoveries: true})
	if err != nil {
		t.Fatal(err)
	}
	claim, err = owner.ClaimOutbox(ctx, OutboxClaimRequest{Version: ContractVersion, WorkerID: "discord", Limit: 25, Channel: "discord"})
	if err != nil {
		t.Fatal(err)
	}
	if len(claim.Intents) != 0 {
		t.Fatal("disabled channel retained pending alerts")
	}
}
func TestDiscordURLUsesPublicIncidentRoute(t *testing.T) {
	got := discordIncidentURL("https://status.example.test/api/unsubscribe", "auto/server/123")
	if got != "https://status.example.test/incident/auto%2Fserver%2F123" {
		t.Fatal(got)
	}
}

func TestDiscordMinorIncidentDoesNotClaimServiceUnavailable(t *testing.T) {
	ctx := context.Background()
	store, owner := openTestOwner(t, ":memory:")
	defer store.Close()
	setDeliveryConfig(t, owner, "performance-base", "https://status.example.test/unsubscribe")
	if _, err := owner.SetDiscordConfig(ctx, DiscordConfig{Version: ContractVersion, Enabled: true, Outages: true, Recoveries: true}); err != nil {
		t.Fatal(err)
	}
	command := incidentTransitionResult("performance-open", false)
	command.Transitions[0].Incident.Severity = "minor"
	command.Transitions[0].Incident.Title = "Game performance — expect lag"
	if _, err := owner.ApplyTransition(ctx, command); err != nil {
		t.Fatal(err)
	}
	claim, err := owner.ClaimOutbox(ctx, OutboxClaimRequest{Version: ContractVersion, WorkerID: "performance", Limit: 25, Channel: "discord"})
	if err != nil {
		t.Fatal(err)
	}
	if len(claim.Intents) != 1 || !strings.Contains(claim.Intents[0].Body, "Service degraded") || strings.Contains(claim.Intents[0].Body, "service is unavailable") {
		t.Fatal("performance alert was described as an outage")
	}
}
