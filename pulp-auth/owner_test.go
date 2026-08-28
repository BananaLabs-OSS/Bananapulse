package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

func openTestOwner(t *testing.T, dsn string) (*Store, *Owner) {
	t.Helper()
	store, err := OpenStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := OpenOwner(store)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store, owner
}

func importAdmin(t *testing.T, owner *Owner, at time.Time) {
	t.Helper()
	result, err := owner.ImportIdentity(context.Background(), AdminIdentityImportRequest{
		Version: ContractVersion, RequestID: "identity-import-1", IdentityID: "admin_1",
		Email: "Admin@Example.com", State: IdentityEnabled, ImportedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Imported || result.IdentityID != "admin_1" {
		t.Fatalf("unexpected identity result: %#v", result)
	}
}

func issueAndConsume(t *testing.T, owner *Owner, at time.Time, suffix string) MagicLinkConsumeResult {
	t.Helper()
	issued, err := owner.IssueMagicLink(context.Background(), MagicLinkIssueRequest{
		Version: ContractVersion, RequestID: "link-issue-" + suffix, Email: "admin@example.com",
		Token: "magic-plaintext-" + suffix, IssuedAt: at, ExpiresAt: at.Add(15 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !issued.Accepted || !issued.Deliver || issued.ChallengeID == "" {
		t.Fatalf("unexpected issue result: %#v", issued)
	}
	consumed, err := owner.ConsumeMagicLink(context.Background(), MagicLinkConsumeRequest{
		Version: ContractVersion, RequestID: "link-consume-" + suffix,
		Token: "magic-plaintext-" + suffix, ConsumedAt: at.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !consumed.Authenticated || consumed.IdentityID != "admin_1" || consumed.ChallengeID != issued.ChallengeID {
		t.Fatalf("unexpected consume result: %#v", consumed)
	}
	return consumed
}

func TestAdminMagicLinkSessionLifecycleSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 7, 26, 1, 0, 0, 0, time.UTC)
	dsn := filepath.Join(t.TempDir(), "auth.db")
	store, owner := openTestOwner(t, dsn)
	importAdmin(t, owner, at)

	consumed := issueAndConsume(t, owner, at, "restart")
	sessionRequest := SessionCreateRequest{
		Version: ContractVersion, RequestID: "session-create-1", ChallengeID: consumed.ChallengeID,
		IdentityID: consumed.IdentityID, Token: "session-plaintext", IssuedAt: at.Add(time.Minute),
		ExpiresAt: at.Add(7 * 24 * time.Hour),
	}
	session, err := owner.CreateSession(ctx, sessionRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !session.Created || session.SessionID == "" {
		t.Fatalf("unexpected session result: %#v", session)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, owner = openTestOwner(t, dsn)
	defer store.Close()
	replayed, err := owner.CreateSession(ctx, sessionRequest)
	if err != nil {
		t.Fatal(err)
	}
	if replayed != session {
		t.Fatalf("restart replay drifted: %#v != %#v", replayed, session)
	}
	validated, err := owner.ValidateSession(ctx, SessionValidateRequest{
		Version: ContractVersion, Token: "session-plaintext", At: at.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !validated.Valid || validated.IdentityID != "admin_1" || validated.Email != "admin@example.com" || validated.Role != "admin" {
		t.Fatalf("unexpected validation result: %#v", validated)
	}

	revoked, err := owner.RevokeSession(ctx, SessionRevokeRequest{
		Version: ContractVersion, RequestID: "session-revoke-1", Token: "session-plaintext",
		ActorID: "admin_1", RevokedAt: at.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !revoked.Revoked {
		t.Fatal("expected session revocation")
	}
	validated, err = owner.ValidateSession(ctx, SessionValidateRequest{
		Version: ContractVersion, Token: "session-plaintext", At: at.Add(3 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if validated.Valid {
		t.Fatal("revoked session remained valid")
	}
}

func TestMagicLinkIsOneTimeExpiringAndAntiEnumerating(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 7, 26, 2, 0, 0, 0, time.UTC)
	store, owner := openTestOwner(t, ":memory:")
	defer store.Close()
	importAdmin(t, owner, at)

	unknown, err := owner.IssueMagicLink(ctx, MagicLinkIssueRequest{
		Version: ContractVersion, RequestID: "unknown-issue", Email: "nobody@example.com",
		Token: "unknown-token", IssuedAt: at, ExpiresAt: at.Add(15 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !unknown.Accepted || unknown.Deliver || unknown.ChallengeID != "" {
		t.Fatalf("unknown identity leaked state: %#v", unknown)
	}

	issued, err := owner.IssueMagicLink(ctx, MagicLinkIssueRequest{
		Version: ContractVersion, RequestID: "race-issue", Email: "admin@example.com",
		Token: "race-token", IssuedAt: at, ExpiresAt: at.Add(15 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	const consumers = 8
	results := make(chan MagicLinkConsumeResult, consumers)
	errorsFound := make(chan error, consumers)
	var wait sync.WaitGroup
	for i := 0; i < consumers; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			result, err := owner.ConsumeMagicLink(ctx, MagicLinkConsumeRequest{
				Version: ContractVersion, RequestID: "race-consume-" + string(rune('a'+index)),
				Token: "race-token", ConsumedAt: at.Add(time.Minute),
			})
			if err != nil {
				errorsFound <- err
				return
			}
			results <- result
		}(i)
	}
	wait.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	authenticated := 0
	for result := range results {
		if result.Authenticated {
			authenticated++
			if result.ChallengeID != issued.ChallengeID {
				t.Fatalf("wrong challenge: %#v", result)
			}
		}
	}
	if authenticated != 1 {
		t.Fatalf("authenticated %d consumers, want 1", authenticated)
	}

	expiredIssue, err := owner.IssueMagicLink(ctx, MagicLinkIssueRequest{
		Version: ContractVersion, RequestID: "expired-issue", Email: "admin@example.com",
		Token: "expired-token", IssuedAt: at, ExpiresAt: at.Add(time.Minute),
	})
	if err != nil || !expiredIssue.Deliver {
		t.Fatalf("issue expired fixture: %#v, %v", expiredIssue, err)
	}
	expired, err := owner.ConsumeMagicLink(ctx, MagicLinkConsumeRequest{
		Version: ContractVersion, RequestID: "expired-consume", Token: "expired-token", ConsumedAt: at.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if expired.Authenticated {
		t.Fatal("expired challenge authenticated")
	}
}

func TestRequestIdempotencyRejectsPayloadSubstitution(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 7, 26, 3, 0, 0, 0, time.UTC)
	store, owner := openTestOwner(t, ":memory:")
	defer store.Close()
	importAdmin(t, owner, at)
	request := MagicLinkIssueRequest{
		Version: ContractVersion, RequestID: "stable-issue", Email: "admin@example.com",
		Token: "stable-token", IssuedAt: at, ExpiresAt: at.Add(time.Minute),
	}
	first, err := owner.IssueMagicLink(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := owner.IssueMagicLink(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("idempotent response drifted: %#v != %#v", first, second)
	}
	request.Token = "substitution-token"
	if _, err := owner.IssueMagicLink(ctx, request); err == nil || !strings.Contains(err.Error(), "different command") {
		t.Fatalf("expected request substitution rejection, got %v", err)
	}
}

func TestAPITokensEnforceScopeExpiryRevocationAndDigestImport(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 7, 26, 4, 0, 0, 0, time.UTC)
	store, owner := openTestOwner(t, ":memory:")
	defer store.Close()

	issued, err := owner.IssueAPIToken(ctx, APITokenIssueRequest{
		Version: ContractVersion, RequestID: "api-issue-read", TokenID: "api_read", Name: "Read probe",
		Scope: "read", Token: "api-read-plaintext", CreatedAt: at,
	})
	if err != nil || !issued.Created {
		t.Fatalf("issue token: %#v, %v", issued, err)
	}
	denied, err := owner.ValidateAPIToken(ctx, APITokenValidateRequest{
		Version: ContractVersion, RequestID: "api-validate-write", Token: "api-read-plaintext",
		RequiredScope: "write", ValidatedAt: at.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if denied.Valid || denied.TokenID != "" || denied.Scope != "" {
		t.Fatalf("scope denial leaked metadata: %#v", denied)
	}
	valid, err := owner.ValidateAPIToken(ctx, APITokenValidateRequest{
		Version: ContractVersion, RequestID: "api-validate-read", Token: "api-read-plaintext",
		RequiredScope: "read", ValidatedAt: at.Add(2 * time.Minute),
	})
	if err != nil || !valid.Valid || valid.TokenID != "api_read" {
		t.Fatalf("validate token: %#v, %v", valid, err)
	}
	list, err := owner.ListAPITokens(ctx, APITokenListRequest{Version: ContractVersion})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tokens) != 1 || list.Tokens[0].LastUsedAt == nil || !list.Tokens[0].LastUsedAt.Equal(at.Add(2*time.Minute)) {
		t.Fatalf("last-used metadata missing: %#v", list)
	}
	revoked, err := owner.RevokeAPIToken(ctx, APITokenRevokeRequest{
		Version: ContractVersion, RequestID: "api-revoke-read", TokenID: "api_read",
		ActorID: "admin_1", RevokedAt: at.Add(3 * time.Minute),
	})
	if err != nil || !revoked.Revoked {
		t.Fatalf("revoke token: %#v, %v", revoked, err)
	}
	afterRevoke, err := owner.ValidateAPIToken(ctx, APITokenValidateRequest{
		Version: ContractVersion, RequestID: "api-validate-revoked", Token: "api-read-plaintext",
		RequiredScope: "read", ValidatedAt: at.Add(4 * time.Minute),
	})
	if err != nil || afterRevoke.Valid {
		t.Fatalf("revoked token validated: %#v, %v", afterRevoke, err)
	}

	legacyExpiry := at.Add(time.Hour)
	imported, err := owner.ImportAPIToken(ctx, APITokenImportRequest{
		Version: ContractVersion, RequestID: "api-import-legacy", TokenID: "api_legacy", Name: "Legacy full",
		Scope: "full", TokenDigest: digest("legacy-api-token"), CreatedAt: at, ExpiresAt: &legacyExpiry,
	})
	if err != nil || !imported.Created {
		t.Fatalf("import digest: %#v, %v", imported, err)
	}
	legacy, err := owner.ValidateAPIToken(ctx, APITokenValidateRequest{
		Version: ContractVersion, RequestID: "api-validate-legacy", Token: "legacy-api-token",
		RequiredScope: "full", ValidatedAt: at.Add(30 * time.Minute),
	})
	if err != nil || !legacy.Valid || legacy.TokenID != "api_legacy" {
		t.Fatalf("validate imported digest: %#v, %v", legacy, err)
	}
	expired, err := owner.ValidateAPIToken(ctx, APITokenValidateRequest{
		Version: ContractVersion, RequestID: "api-validate-expired", Token: "legacy-api-token",
		RequiredScope: "read", ValidatedAt: legacyExpiry,
	})
	if err != nil || expired.Valid {
		t.Fatalf("expired token validated: %#v, %v", expired, err)
	}
	bad := APITokenImportRequest{
		Version: ContractVersion, RequestID: "api-import-bad", TokenID: "bad", Name: "Bad",
		Scope: "read", TokenDigest: strings.ToUpper(digest("bad")), CreatedAt: at,
	}
	if _, err := owner.ImportAPIToken(ctx, bad); err == nil {
		t.Fatal("uppercase digest was accepted")
	}
}

func TestSourceCredentialsImportRotateValidateAndAssertSubject(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 7, 26, 5, 0, 0, 0, time.UTC)
	store, owner := openTestOwner(t, ":memory:")
	defer store.Close()

	imported, err := owner.ImportSourceCredential(ctx, SourceCredentialImportRequest{
		Version: ContractVersion, RequestID: "source-import", CredentialID: "cred_old",
		SourceID: "source_uptime", TokenDigest: digest("source-old-token"), CreatedAt: at,
	})
	if err != nil || !imported.Created {
		t.Fatalf("import source credential: %#v, %v", imported, err)
	}
	valid, err := owner.ValidateSourceCredential(ctx, SourceCredentialValidateRequest{
		Version: ContractVersion, RequestID: "source-validate-old", Token: "source-old-token",
		ValidatedAt: at.Add(time.Minute),
	})
	if err != nil || !valid.Valid || valid.SourceID != "source_uptime" || valid.CredentialID != "cred_old" {
		t.Fatalf("validate source credential: %#v, %v", valid, err)
	}
	rotated, err := owner.RotateSourceCredential(ctx, SourceCredentialRotateRequest{
		Version: ContractVersion, RequestID: "source-rotate", PreviousTokenID: "cred_old",
		CredentialID: "cred_new", SourceID: "source_uptime", Token: "source-new-token",
		ActorID: "admin_1", RotatedAt: at.Add(2 * time.Minute),
	})
	if err != nil || !rotated.Created {
		t.Fatalf("rotate source credential: %#v, %v", rotated, err)
	}
	old, err := owner.ValidateSourceCredential(ctx, SourceCredentialValidateRequest{
		Version: ContractVersion, RequestID: "source-validate-revoked", Token: "source-old-token",
		ValidatedAt: at.Add(3 * time.Minute),
	})
	if err != nil || old.Valid || old.SourceID != "" {
		t.Fatalf("old source credential validated or leaked: %#v, %v", old, err)
	}
	current, err := owner.ValidateSourceCredential(ctx, SourceCredentialValidateRequest{
		Version: ContractVersion, RequestID: "source-validate-new", Token: "source-new-token",
		ValidatedAt: at.Add(3 * time.Minute),
	})
	if err != nil || !current.Valid || current.SourceID != "source_uptime" {
		t.Fatalf("new source credential invalid: %#v, %v", current, err)
	}
}

func TestSourceCredentialRevokeIsIdempotentAndPayloadFenced(t *testing.T) {
	store, owner := openTestOwner(t, ":memory:")
	defer store.Close()
	ctx := context.Background()
	if _, err := owner.RotateSourceCredential(ctx, SourceCredentialRotateRequest{
		Version: ContractVersion, RequestID: "source-create", CredentialID: "cred-active",
		SourceID: "source-probe", Token: "source-secret", ActorID: "admin",
		RotatedAt: time.Date(2026, 7, 26, 1, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	request := SourceCredentialRevokeRequest{
		Version: ContractVersion, RequestID: "source-revoke", SourceID: "source-probe",
		ActorID: "admin", RevokedAt: time.Date(2026, 7, 26, 1, 1, 0, 0, time.UTC),
	}
	first, err := owner.RevokeSourceCredential(ctx, request)
	if err != nil || !first.Revoked || first.SourceID != "source-probe" {
		t.Fatalf("first revoke = %#v, %v", first, err)
	}
	replayed, err := owner.RevokeSourceCredential(ctx, request)
	if err != nil || replayed != first {
		t.Fatalf("replayed revoke = %#v, %v", replayed, err)
	}
	changed := request
	changed.SourceID = "source-other"
	if _, err := owner.RevokeSourceCredential(ctx, changed); err == nil {
		t.Fatal("expected payload-fenced request-id reuse to fail")
	}
	validated, err := owner.ValidateSourceCredential(ctx, SourceCredentialValidateRequest{
		Version: ContractVersion, RequestID: "source-after-revoke", Token: "source-secret",
		ValidatedAt: time.Date(2026, 7, 26, 1, 2, 0, 0, time.UTC),
	})
	if err != nil || validated.Valid {
		t.Fatalf("validation after revoke = %#v, %v", validated, err)
	}
}

func TestProjectionAndAuditAreCredentialSafe(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 7, 26, 6, 0, 0, 0, time.UTC)
	store, owner := openTestOwner(t, ":memory:")
	defer store.Close()
	importAdmin(t, owner, at)
	issueAndConsume(t, owner, at, "privacy")
	if _, err := owner.IssueAPIToken(ctx, APITokenIssueRequest{
		Version: ContractVersion, RequestID: "privacy-api", TokenID: "privacy_api", Name: "Privacy",
		Scope: "read", Token: "privacy-api-plaintext", CreatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ImportSourceCredential(ctx, SourceCredentialImportRequest{
		Version: ContractVersion, RequestID: "privacy-source", CredentialID: "privacy_source",
		SourceID: "source_private", Token: "privacy-source-plaintext", CreatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}

	projection, err := owner.Projection(ctx, ProjectionRequest{Version: ContractVersion, At: at.Add(2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if projection.EnabledIdentityCount != 1 || projection.ActiveAPITokenCount != 1 || projection.ActiveSourceCredentialCount != 1 {
		t.Fatalf("unexpected projection: %#v", projection)
	}
	rawProjection, err := msgpack.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"admin@example.com", "privacy-api-plaintext", "privacy-source-plaintext", "source_private"} {
		if bytes.Contains(rawProjection, []byte(secret)) {
			t.Fatalf("projection contains private value %q", secret)
		}
	}

	audit, err := owner.QueryAudit(ctx, AdminAuditQueryRequest{Version: ContractVersion, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.Events) == 0 {
		t.Fatal("expected audit events")
	}
	rawAudit, err := msgpack.Marshal(audit)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"admin@example.com", "magic-plaintext-privacy", "privacy-api-plaintext", "privacy-source-plaintext"} {
		if bytes.Contains(rawAudit, []byte(secret)) {
			t.Fatalf("audit contains credential or email %q", secret)
		}
	}

	for _, table := range []string{"auth_magic_link_challenges", "auth_sessions", "auth_api_tokens", "auth_source_credentials"} {
		var concatenated string
		query := "SELECT COALESCE(GROUP_CONCAT(token_hash, ','),'') FROM " + table
		if err := store.DB().QueryRowContext(ctx, query).Scan(&concatenated); err != nil {
			t.Fatal(err)
		}
		for _, plaintext := range []string{"magic-plaintext-privacy", "privacy-api-plaintext", "privacy-source-plaintext"} {
			if strings.Contains(concatenated, plaintext) {
				t.Fatalf("%s persisted plaintext credential", table)
			}
		}
	}
}
