//go:build ownerparitye2e

package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	ownerParityAdminEmail  = "owner-parity-admin@example.test"
	ownerParityAuthVersion = "credential-registry/v1"
)

// TestOwnerParityAstroNodeToPulpOwners is the local, Docker-free equivalent of
// deployment/compose.local.yaml + compose.owner-parity.yaml. It starts the real
// Pulp application/bridge and the packaged Astro Node server, deliberately
// points DATABASE_URL at an unreachable local address, and exercises the HTTP
// compatibility surface. Therefore any successful route has crossed:
//
//	Astro HTTP -> authenticated bridge -> Pulp -> Lua -> owner WASM
//
// rather than silently falling back to the legacy Postgres model.
func TestOwnerParityAstroNodeToPulpOwners(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	astroAddress := freeLoopbackAddress(t)
	astroURL := "http://" + astroAddress
	buildAstroNodeServer(t, repoRoot, astroURL)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	t.Setenv(legacyImportEnabledEnv, "false")
	t.Setenv(legacyImportReverifyEnv, "false")
	t.Setenv(legacyImportSourceDSNEnv, "")
	t.Setenv(legacyImportFenceEnv, "")

	bridgeAddress := freeLoopbackAddress(t)
	bridgeToken := randomTestCredential(t, 32)
	storageRoot := t.TempDir()
	config := bridgeConfig{
		Address:     bridgeAddress,
		Token:       bridgeToken,
		AppManifest: filepath.Join(repoRoot, "application", "pulp.app.toml"),
		StorageRoot: storageRoot,
		Families: bridgeFamilies{
			monitorAdmin: true, monitorIngest: true, monitorSweep: true,
			subscriberAdmin: true, migration: false,
			auth: true, authAdmin: true, sourceAdmin: true,
		},
		UnsubscribeBaseURL: "https://status.example.test/unsubscribe",
	}

	first, err := startBridge(ctx, config)
	if err != nil {
		t.Fatalf("start first owner bridge: %v", err)
	}
	bridgeURL := "http://" + bridgeAddress
	waitForHTTP(t, ctx, bridgeURL+"/healthz", http.StatusOK)

	now := time.Now().UTC().Truncate(time.Second)
	rootCommand := ownerParityComponentCommand(
		"owner-parity-root-v1", "example", "", "Example Co", "organization", now,
	)
	serviceCommand := ownerParityComponentCommand(
		"owner-parity-service-v1", "api", "example", "API Service", "service", now,
	)
	postOwnerCommand(t, bridgeURL, bridgeToken, rootCommand, false)
	postOwnerCommand(t, bridgeURL, bridgeToken, serviceCommand, false)
	postOwnerCommand(t, bridgeURL, bridgeToken, bridgeMonitorCommand{
		Version: "monitor.v1", ID: "owner-parity-incident-v1", Kind: "open_incident", AtUnix: now.Unix(),
		Incident: &bridgeMonitorIncident{
			ID: "owner-parity-incident", Title: "Owner parity API incident",
			Summary: "HTTP owner parity is under verification.", Status: "investigating",
			Severity: "moderate", Affects: []string{"api"}, Auto: false,
			StartedAtUnix: now.Unix(), CreatedAtUnix: now.Unix(),
		},
	}, false)
	postOwnerCommand(t, bridgeURL, bridgeToken, bridgeMonitorCommand{
		Version: "monitor.v1", ID: "owner-parity-update-v1", Kind: "update_incident", AtUnix: now.Add(time.Minute).Unix(),
		Update: &bridgeIncidentUpdate{
			ID: "owner-parity-update", IncidentID: "owner-parity-incident",
			AtUnix: now.Add(time.Minute).Unix(), Label: "monitoring",
			Body: "Owner-backed recovery is progressing.", Author: ownerParityAdminEmail,
		},
	}, false)
	postOwnerCommand(t, bridgeURL, bridgeToken, bridgeMonitorCommand{
		Version: "monitor.v1", ID: "owner-parity-maintenance-v1", Kind: "schedule_maintenance", AtUnix: now.Unix(),
		Maintenance: &bridgeMaintenance{
			ID: "owner-parity-maintenance", Title: "Owner parity maintenance",
			Summary: "Verifies maintenance projection parity.", Kind: "scheduled",
			ScheduledStartUnix: now.Add(time.Hour).Unix(),
			ScheduledEndUnix:   now.Add(2 * time.Hour).Unix(),
			Affects:            []string{"api"}, CreatedAtUnix: now.Unix(),
		},
	}, false)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 20*time.Second)
	if err := first.shutdown(shutdownCtx); err != nil {
		shutdownCancel()
		t.Fatalf("shutdown first owner bridge: %v", err)
	}
	shutdownCancel()

	second, err := startBridge(ctx, config)
	if err != nil {
		t.Fatalf("restart owner bridge: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer shutdownCancel()
		if err := second.shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown restarted owner bridge: %v", err)
		}
	})
	waitForHTTP(t, ctx, bridgeURL+"/healthz", http.StatusOK)
	postOwnerCommand(t, bridgeURL, bridgeToken, serviceCommand, true)

	adminSessionSecret := randomTestCredential(t, 32)
	adminSessionToken := createOwnerAdminSession(t, bridgeURL, bridgeToken, now)
	adminCookie := signedAdminCookie(adminSessionToken, adminSessionSecret)

	node := startAstroNodeServer(t, ctx, repoRoot, astroAddress, map[string]string{
		"PULP_BRIDGE_URL":                      bridgeURL,
		"PULP_BRIDGE_TOKEN":                    bridgeToken,
		"PULP_MONITOR_OWNER_ENABLED":           "true",
		"PULP_MONITOR_ADMIN_OWNER_ENABLED":     "true",
		"PULP_INGEST_OWNER_ENABLED":            "true",
		"PULP_INCIDENTS_OWNER_ENABLED":         "true",
		"PULP_MAINTENANCE_OWNER_ENABLED":       "true",
		"PULP_SWEEP_OWNER_ENABLED":             "true",
		"PULP_SUBSCRIBERS_OWNER_ENABLED":       "true",
		"PULP_SUBSCRIBERS_ADMIN_OWNER_ENABLED": "true",
		"PULP_AUTH_OWNER_ENABLED":              "true",
		"PULP_SUBSCRIBER_TOKEN_SECRET":         randomTestCredential(t, 32),
		"ADMIN_SESSION_SECRET":                 adminSessionSecret,
		"ADMIN_EMAIL":                          ownerParityAdminEmail,
		"SITE_URL":                             astroURL,
		"DATABASE_URL":                         "postgres://owner-parity-forbidden@127.0.0.1:1/legacy?connect_timeout=1",
	})
	t.Cleanup(node.stop)
	waitForHTTP(t, ctx, astroURL+"/", http.StatusOK)

	assertOwnerPublicParity(t, astroURL)

	// These mutations must authenticate with pulp-auth and then use monitor
	// owner commands. The deliberately unreachable DATABASE_URL turns any
	// legacy auth or validation fallback into an immediate, actionable failure.
	component := postAstroJSON(t, astroURL+"/api/v1/admin/components", adminCookie, map[string]any{
		"id": "admin-created", "name": "Admin Created Service", "kind": "service", "parentId": "example",
	}, http.StatusCreated)
	assertJSONPathString(t, component, []string{"data", "id"}, "admin-created")

	incident := postAstroJSON(t, astroURL+"/api/v1/admin/incidents", adminCookie, map[string]any{
		"title": "Admin owner incident", "summary": "Created through the Astro admin compatibility route.",
		"severity": "moderate", "affects": []string{"admin-created"},
	}, http.StatusCreated)
	incidentID := jsonPathString(t, incident, "data", "id")
	if incidentID == "" {
		t.Fatal("admin incident response omitted data.id")
	}
	postAstroJSON(t, astroURL+"/api/v1/admin/incidents/"+incidentID+"/updates", adminCookie, map[string]any{
		"label": "monitoring", "body": "Admin owner update.",
	}, http.StatusCreated)

	maintenance := postAstroJSON(t, astroURL+"/api/v1/admin/maintenance", adminCookie, map[string]any{
		"title": "Admin owner maintenance", "summary": "Created through the owner route.",
		"scheduledStart": now.Add(3 * time.Hour).Format(time.RFC3339),
		"scheduledEnd":   now.Add(4 * time.Hour).Format(time.RFC3339),
		"affects":        []string{"admin-created"},
	}, http.StatusCreated)
	maintenanceID := jsonPathString(t, maintenance, "data", "id")
	if maintenanceID == "" {
		t.Fatal("admin maintenance response omitted data.id")
	}
	patchAstroJSON(t, astroURL+"/api/v1/admin/maintenance/"+maintenanceID, adminCookie, map[string]any{
		"summary": "Updated through the owner route.",
	}, http.StatusOK)

	// Owner state is immediately visible through the public compatibility
	// surface; a legacy write could not satisfy this assertion because the
	// legacy database is unreachable by construction.
	assertHTTPBodyContains(t, astroURL+"/history", http.StatusOK, "Admin owner incident")
}

func ownerParityComponentCommand(commandID, id, parentID, name, kind string, at time.Time) bridgeMonitorCommand {
	return bridgeMonitorCommand{
		Version: "monitor.v1", ID: commandID, Kind: "upsert_component", AtUnix: at.Unix(),
		Component: &bridgeMonitorComponent{
			ID: id, ParentID: parentID, Name: name, Kind: kind,
			FallbackStatus: "operational", Launched: true, LaunchedSet: true,
		},
	}
}

func postOwnerCommand(
	t *testing.T,
	bridgeURL, token string,
	command bridgeMonitorCommand,
	wantDeduped bool,
) {
	t.Helper()
	var result monitorCommandResult
	postBridgeEvent(t, bridgeURL, token, eventMonitorAdminCommand, command, &result)
	if result.CommandID != command.ID || result.Revision == 0 || result.Deduped != wantDeduped {
		t.Fatalf("owner command %q result = %#v, want deduped=%v", command.ID, result, wantDeduped)
	}
}

func createOwnerAdminSession(t *testing.T, bridgeURL, bridgeToken string, now time.Time) string {
	t.Helper()
	var imported struct {
		IdentityID string `json:"identity_id"`
		Imported   bool   `json:"imported"`
	}
	postBridgeEvent(t, bridgeURL, bridgeToken, eventHostAuthAdminIdentityImport, bridgeAuthAdminIdentityImportRequest{
		Version: ownerParityAuthVersion, RequestID: "owner-parity-admin-import",
		IdentityID: "owner-parity-admin", Email: ownerParityAdminEmail, State: "enabled",
		ActorID: "owner-parity-e2e", ImportedAt: now,
	}, &imported)
	if !imported.Imported || imported.IdentityID != "owner-parity-admin" {
		t.Fatalf("admin identity import = %#v", imported)
	}

	magicToken := randomTestCredential(t, 32)
	var issued struct {
		Accepted    bool   `json:"accepted"`
		Deliver     bool   `json:"deliver"`
		ChallengeID string `json:"challenge_id"`
	}
	postBridgeEvent(t, bridgeURL, bridgeToken, eventHostAuthMagicLinkIssue, bridgeAuthMagicLinkIssueRequest{
		Version: ownerParityAuthVersion, RequestID: "owner-parity-magic-issue",
		Email: ownerParityAdminEmail, Token: magicToken,
		IssuedAt: now, ExpiresAt: now.Add(15 * time.Minute),
	}, &issued)
	if !issued.Accepted || !issued.Deliver || issued.ChallengeID == "" {
		t.Fatalf("admin magic-link issue = %#v", issued)
	}

	var consumed struct {
		Authenticated bool   `json:"authenticated"`
		ChallengeID   string `json:"challenge_id"`
		IdentityID    string `json:"identity_id"`
	}
	postBridgeEvent(t, bridgeURL, bridgeToken, eventHostAuthMagicLinkConsume, bridgeAuthMagicLinkConsumeRequest{
		Version: ownerParityAuthVersion, RequestID: "owner-parity-magic-consume",
		Token: magicToken, ConsumedAt: now.Add(time.Minute),
	}, &consumed)
	if !consumed.Authenticated || consumed.ChallengeID != issued.ChallengeID ||
		consumed.IdentityID != "owner-parity-admin" {
		t.Fatalf("admin magic-link consume = %#v", consumed)
	}

	sessionToken := randomTestCredential(t, 32)
	var created struct {
		SessionID string `json:"session_id"`
		Created   bool   `json:"created"`
	}
	postBridgeEvent(t, bridgeURL, bridgeToken, eventHostAuthSessionCreate, bridgeAuthSessionCreateRequest{
		Version: ownerParityAuthVersion, RequestID: "owner-parity-session-create",
		ChallengeID: consumed.ChallengeID, IdentityID: consumed.IdentityID,
		Token: sessionToken, IssuedAt: now.Add(time.Minute), ExpiresAt: now.Add(24 * time.Hour),
	}, &created)
	if !created.Created || created.SessionID == "" {
		t.Fatalf("admin session create = %#v", created)
	}
	return sessionToken
}

func signedAdminCookie(token, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(token))
	return token + "." + hex.EncodeToString(mac.Sum(nil))
}

func assertOwnerPublicParity(t *testing.T, baseURL string) {
	t.Helper()
	assertHTTPBodyContains(t, baseURL+"/", http.StatusOK, "API Service")
	assertHTTPBodyContains(t, baseURL+"/", http.StatusOK, "Owner parity maintenance")
	assertHTTPBodyContains(t, baseURL+"/history", http.StatusOK, "Owner parity API incident")
	assertHTTPBodyContains(t, baseURL+"/incident/owner-parity-incident", http.StatusOK, "Owner-backed recovery is progressing.")
	assertHTTPBodyContains(t, baseURL+"/feed.atom", http.StatusOK, "Owner parity API incident")
	assertHTTPBodyContains(t, baseURL+"/feed.xml", http.StatusOK, "Owner parity API incident")

	response, err := http.Get(baseURL + "/feed.json")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /feed.json status = %d", response.StatusCode)
	}
	var feed struct {
		Version string `json:"version"`
		Items   []struct {
			Title       string `json:"title"`
			ContentText string `json:"content_text"`
			Status      struct {
				Severity string   `json:"severity"`
				Status   string   `json:"status"`
				Affects  []string `json:"affects"`
			} `json:"_status"`
		} `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&feed); err != nil {
		t.Fatal(err)
	}
	if feed.Version != "https://jsonfeed.org/version/1.1" || len(feed.Items) != 1 {
		t.Fatalf("JSON feed envelope = %#v", feed)
	}
	item := feed.Items[0]
	if item.Title != "Owner parity API incident" ||
		item.ContentText != "HTTP owner parity is under verification." ||
		item.Status.Severity != "moderate" ||
		item.Status.Status != "investigating" ||
		len(item.Status.Affects) != 1 || item.Status.Affects[0] != "api" {
		t.Fatalf("JSON feed parity item = %#v", item)
	}
}

func assertHTTPBodyContains(t *testing.T, url string, status int, needle string) {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status || !bytes.Contains(body, []byte(needle)) {
		t.Fatalf("GET %s = %d %s, want status %d containing %q", url, response.StatusCode, body, status, needle)
	}
}

func postAstroJSON(t *testing.T, url, cookie string, value any, wantStatus int) map[string]any {
	t.Helper()
	return astroJSONRequest(t, http.MethodPost, url, cookie, value, wantStatus)
}

func patchAstroJSON(t *testing.T, url, cookie string, value any, wantStatus int) map[string]any {
	t.Helper()
	return astroJSONRequest(t, http.MethodPatch, url, cookie, value, wantStatus)
}

func astroJSONRequest(
	t *testing.T,
	method, url, cookie string,
	value any,
	wantStatus int,
) map[string]any {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Cookie", "status_admin_session="+cookie)
	request.Header.Set("Origin", request.URL.Scheme+"://"+request.URL.Host)
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf(
			"%s %s = %d %s, want %d; owner parity forbids legacy DB authentication/validation fallback",
			method, response.Request.URL, response.StatusCode, raw, wantStatus,
		)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode %s %s response: %v (%s)", method, url, err, raw)
	}
	return decoded
}

func jsonPathString(t *testing.T, value map[string]any, path ...string) string {
	t.Helper()
	var current any = value
	for _, segment := range path {
		object, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("JSON path %v reached non-object %#v", path, current)
		}
		current = object[segment]
	}
	result, _ := current.(string)
	return result
}

func assertJSONPathString(t *testing.T, value map[string]any, path []string, want string) {
	t.Helper()
	if got := jsonPathString(t, value, path...); got != want {
		t.Fatalf("JSON path %v = %q, want %q", path, got, want)
	}
}

func buildAstroNodeServer(t *testing.T, repoRoot, siteURL string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := npmCommandContext(ctx, "run", "build:local")
	command.Dir = repoRoot
	command.Env = replacedEnvironment(os.Environ(), map[string]string{
		"SITE_URL": siteURL,
	})
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build Astro Node package: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "dist", "server", "entry.mjs")); err != nil {
		t.Fatalf("Astro Node build omitted dist/server/entry.mjs: %v", err)
	}
}

type runningNodeServer struct {
	command *exec.Cmd
	done    chan error
	logs    *bytes.Buffer
}

func startAstroNodeServer(
	t *testing.T,
	ctx context.Context,
	repoRoot, address string,
	values map[string]string,
) *runningNodeServer {
	t.Helper()
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, "node", filepath.Join("dist", "server", "entry.mjs"))
	command.Dir = repoRoot
	values["HOST"] = host
	values["PORT"] = port
	values["NODE_ENV"] = "production"
	command.Env = replacedEnvironment(os.Environ(), values)
	logs := &bytes.Buffer{}
	command.Stdout = logs
	command.Stderr = logs
	if err := command.Start(); err != nil {
		t.Fatalf("start Astro Node server: %v", err)
	}
	node := &runningNodeServer{command: command, done: make(chan error, 1), logs: logs}
	go func() {
		node.done <- command.Wait()
		close(node.done)
	}()
	return node
}

func (node *runningNodeServer) stop() {
	if node == nil || node.command == nil || node.command.Process == nil {
		return
	}
	select {
	case <-node.done:
		return
	default:
		_ = node.command.Process.Kill()
		<-node.done
	}
}

func replacedEnvironment(base []string, values map[string]string) []string {
	keys := make(map[string]struct{}, len(values))
	for key := range values {
		keys[strings.ToUpper(key)] = struct{}{}
	}
	result := make([]string, 0, len(base)+len(values))
	for _, item := range base {
		key, _, found := strings.Cut(item, "=")
		if !found {
			continue
		}
		if _, replace := keys[strings.ToUpper(key)]; !replace {
			result = append(result, item)
		}
	}
	names := make([]string, 0, len(values))
	for key := range values {
		names = append(names, key)
	}
	sort.Strings(names)
	for _, key := range names {
		result = append(result, key+"="+values[key])
	}
	return result
}

func waitForHTTP(t *testing.T, ctx context.Context, url string, wantStatus int) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last string
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			raw, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode == wantStatus {
				return
			}
			last = fmt.Sprintf("status %d: %s", response.StatusCode, raw)
		} else {
			last = err.Error()
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for %s: %v (last: %s)", url, ctx.Err(), last)
		case <-ticker.C:
		}
	}
}

func freeLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func randomTestCredential(t *testing.T, size int) string {
	t.Helper()
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(raw)
}

func npmCommandContext(ctx context.Context, args ...string) *exec.Cmd {
	name := "npm"
	if runtime.GOOS == "windows" {
		name = "npm.cmd"
	}
	return exec.CommandContext(ctx, name, args...)
}
