package main

import (
	"context"
	"github.com/BananaLabs-OSS/Pulp/run"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiscordAdminAndDeliveryWithRealOwners(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	observer := &providerObserver{ready: make(chan run.ApplicationProviderAccess, 1)}
	runtime, err := run.NewDirectApplicationRuntime(filepath.Join("..", "application", "pulp.app.toml"), run.DirectApplicationOptions{StorageRoot: t.TempDir(), Lifecycle: observer})
	if err != nil {
		t.Fatal(err)
	}
	if err = runtime.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(ctx)
	client, err := newApplicationClient(<-observer.ready)
	if err != nil {
		t.Fatal(err)
	}
	if err = configureSubscriberDelivery(ctx, client, "https://status.example.test/unsubscribe"); err != nil {
		t.Fatal(err)
	}
	d, err := newDiscordDelivery(t.TempDir(), client)
	if err != nil {
		t.Fatal(err)
	}
	defer d.db.Close()
	bridge, err := newHTTPBridgeWithFamilies(client, "discord-test-token", bridgeFamilies{authAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	bridge.discord = d
	server := httptest.NewServer(bridge.Handler())
	defer server.Close()
	response, err := http.Post(server.URL+bridgeEventsPath+discordSettingsEvent, "application/json", strings.NewReader(`{"action":"get"}`))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("missing bridge authentication")
	}
	var view discordSettingsView
	postBridgeEvent(t, server.URL, "discord-test-token", discordSettingsEvent, discordSettingsRequest{Action: "save", WebhookURL: testWebhook, Enabled: true, Outages: true, Recoveries: true}, &view)
	if !view.Configured || !view.Enabled {
		t.Fatalf("settings: %#v", view)
	}
	postBridgeEvent(t, server.URL, "discord-test-token", discordSettingsEvent, discordSettingsRequest{Action: "save", Enabled: true, Outages: true, Recoveries: true}, &view)
	if !view.Configured {
		t.Fatal("blank update removed saved webhook")
	}
	var result monitorCommandResult
	if err = client.callRaw(ctx, eventMonitorCommand, monitorCommand{Version: "monitor.v1", ID: "discord-component", Kind: "upsert_component", Component: &monitorComponent{ID: "discord-server", Name: "Game server", Kind: "service", Critical: true, FallbackStatus: "operational"}}, &result); err != nil {
		t.Fatal(err)
	}
	command := monitorCommand{Version: "monitor.v1", ID: "discord-incident", Kind: "open_incident", AtUnix: 1800000000, Incident: &monitorIncident{ID: "auto/server/123", Title: "Game server", Summary: "Raw monitoring address must remain private", Status: "investigating", Severity: "major", Affects: []string{"discord-server"}, StartedAtUnix: 1800000000}}
	if err = client.callRaw(ctx, eventMonitorAdminCommand, command, &result); err != nil {
		t.Fatal(err)
	}
	if err = client.callRaw(ctx, eventMonitorAdminCommand, command, &result); err != nil {
		t.Fatal(err)
	}
	calls := 0
	d.http.Transport = discordTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), "allowed_mentions") || strings.Contains(string(raw), "Raw monitoring") {
			t.Fatal("unsafe Discord payload")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})
	worker, _ := newEmailOutboxWorker(client, d)
	worker.channel = "discord"
	drained, err := worker.DrainOnce(ctx, "discord-e2e", 25)
	if err != nil {
		t.Fatal(err)
	}
	if drained.Delivered != 1 || calls != 1 {
		t.Fatalf("delivery: %#v calls=%d", drained, calls)
	}
	drained, err = worker.DrainOnce(ctx, "discord-e2e", 25)
	if err != nil {
		t.Fatal(err)
	}
	if drained.Claimed != 0 {
		t.Fatal("settled intent was replayed")
	}
	postBridgeEvent(t, server.URL, "discord-test-token", discordSettingsEvent, discordSettingsRequest{Action: "save", Remove: true, Outages: true, Recoveries: true}, &view)
	if view.Configured || view.Enabled {
		t.Fatal("webhook removal did not disable channel")
	}
	bridge.families.authAdmin = false
	request, _ := http.NewRequest(http.MethodPost, server.URL+bridgeEventsPath+discordSettingsEvent, strings.NewReader(`{"action":"get"}`))
	request.Header.Set(bridgeTokenHeader, "discord-test-token")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatalf("disabled family accepted settings: %d", response.StatusCode)
	}
}
