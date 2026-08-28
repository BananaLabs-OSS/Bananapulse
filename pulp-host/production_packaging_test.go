package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestLocalProductionCompositionIsFailClosed(t *testing.T) {
	raw, err := os.ReadFile("../deployment/compose.local.yaml")
	if err != nil {
		t.Fatal(err)
	}
	compose := string(raw)
	for _, name := range []string{
		bridgeMonitorAdminEnv,
		bridgeMonitorIngestEnv,
		bridgeMonitorSweepEnv,
		bridgeSubscriberAdminEnv,
		bridgeMigrationEnv,
		bridgeAuthEnv,
		bridgeAuthAdminEnv,
		bridgeSourceAdminEnv,
		legacyImportEnabledEnv,
		legacyImportReverifyEnv,
	} {
		if !strings.Contains(compose, name+`: "false"`) {
			t.Fatalf("local production composition does not explicitly disable %s", name)
		}
	}
	if strings.Contains(compose, legacyImportSourceDSNEnv) ||
		strings.Contains(compose, legacyImportFenceEnv) {
		t.Fatal("local production composition includes legacy cutover inputs")
	}
	if strings.Count(compose, "PULP_BRIDGE_TOKEN: ${PULP_BRIDGE_TOKEN:?") != 2 {
		t.Fatal("both services must require the same externally supplied bridge token")
	}
	if strings.Contains(compose, "8788:8788") {
		t.Fatal("local production composition publishes the internal Pulp bridge")
	}
}

func TestPulpHostImagePackagesThePinnedComposition(t *testing.T) {
	raw, err := os.ReadFile("../deployment/Dockerfile.pulp-host")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(raw)
	for _, required := range []string{
		"COPY --from=pulp ",
		"COPY --from=pulp_ext_http ",
		"COPY --from=pulp_ext_sqlite ",
		"COPY --from=pulp_ext_entropy ",
		"COPY --from=pulp_ext_fs ",
		"COPY --from=pulp_ext_udp ",
		"/workspace/Bananapulse/application ./application",
		"/workspace/Bananapulse/pulp-monitor ./pulp-monitor",
		"/workspace/Bananapulse/pulp-subscribers ./pulp-subscribers",
		"/workspace/Bananapulse/pulp-auth ./pulp-auth",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Fatalf("Pulp host image is missing %q", required)
		}
	}
}

func TestOwnerParityCompositionEnablesEveryDeclaredOwnerRoute(t *testing.T) {
	parserRaw, err := os.ReadFile("../src/lib/pulp-bridge.ts")
	if err != nil {
		t.Fatal(err)
	}
	overrideRaw, err := os.ReadFile("../deployment/compose.owner-parity.yaml")
	if err != nil {
		t.Fatal(err)
	}
	override := string(overrideRaw)
	ownerFlagPattern := regexp.MustCompile(`PULP_[A-Z0-9_]+_OWNER_ENABLED`)
	declared := map[string]bool{}
	for _, match := range ownerFlagPattern.FindAllString(string(parserRaw), -1) {
		declared[match] = true
	}
	if len(declared) == 0 {
		t.Fatal("owner route parser declares no parity flags")
	}
	for flag := range declared {
		line := flag + `: "true"`
		if strings.Count(override, line) != 1 {
			t.Fatalf("owner-parity composition must enable %s exactly once", flag)
		}
	}
	for _, bridgeFlag := range []string{
		bridgeMonitorAdminEnv,
		bridgeMonitorIngestEnv,
		bridgeMonitorSweepEnv,
		bridgeSubscriberAdminEnv,
		bridgeAuthEnv,
		bridgeAuthAdminEnv,
		bridgeSourceAdminEnv,
	} {
		if strings.Count(override, bridgeFlag+`: "true"`) != 1 {
			t.Fatalf("owner-parity composition must enable bridge family %s exactly once", bridgeFlag)
		}
	}
	for _, disabled := range []string{
		bridgeMigrationEnv,
		legacyImportEnabledEnv,
		legacyImportReverifyEnv,
	} {
		if strings.Count(override, disabled+`: "false"`) != 1 {
			t.Fatalf("owner-parity composition must keep %s disabled", disabled)
		}
	}
	if strings.Count(override, "PULP_BRIDGE_TOKEN: ${PULP_BRIDGE_TOKEN:?") != 2 {
		t.Fatal("owner-parity composition must require the same external bridge token for both services")
	}
	for _, required := range []string{
		"PULP_SUBSCRIBER_TOKEN_SECRET: ${PULP_SUBSCRIBER_TOKEN_SECRET:?",
		"ADMIN_SESSION_SECRET: ${ADMIN_SESSION_SECRET:?",
		"ADMIN_EMAIL: ${ADMIN_EMAIL:?",
		"PULP_BRIDGE_URL: http://pulp-host:8788",
	} {
		if !strings.Contains(override, required) {
			t.Fatalf("owner-parity composition is missing fail-closed requirement %q", required)
		}
	}
	if strings.Contains(override, "DATABASE_URL") ||
		strings.Contains(override, legacyImportSourceDSNEnv) ||
		strings.Contains(override, legacyImportFenceEnv) {
		t.Fatal("owner-parity composition can route to or activate a legacy data source")
	}
}

func TestConservativeCompositionDoesNotEnableOwnerParity(t *testing.T) {
	defaultRaw, err := os.ReadFile("../deployment/compose.local.yaml")
	if err != nil {
		t.Fatal(err)
	}
	overrideRaw, err := os.ReadFile("../deployment/compose.owner-parity.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defaultCompose := string(defaultRaw)
	ownerFlagPattern := regexp.MustCompile(`PULP_[A-Z0-9_]+_OWNER_ENABLED`)
	for _, flag := range ownerFlagPattern.FindAllString(string(overrideRaw), -1) {
		if strings.Contains(defaultCompose, flag+`: "true"`) {
			t.Fatalf("default composition unexpectedly enables owner parity flag %s", flag)
		}
	}
}
