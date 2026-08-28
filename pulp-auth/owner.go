package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

type Owner struct {
	store *Store
	mu    sync.Mutex
}

func OpenOwner(store *Store) (*Owner, error) {
	if store == nil || store.db == nil {
		return nil, errors.New("auth store is required")
	}
	if err := migrate(context.Background(), store.db); err != nil {
		return nil, fmt.Errorf("migrate auth state: %w", err)
	}
	return &Owner{store: store}, nil
}

func validVersion(version string) error {
	if version != ContractVersion {
		return fmt.Errorf("unsupported auth contract %q", version)
	}
	return nil
}

func require(label, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", label)
	}
	return nil
}

func requireTime(label string, value time.Time) error {
	if value.IsZero() {
		return fmt.Errorf("%s is required", label)
	}
	return nil
}

func normalizedEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) < 3 || len(value) > 320 || strings.Count(value, "@") != 1 || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("invalid email")
	}
	parts := strings.Split(value, "@")
	if parts[0] == "" || parts[1] == "" || !strings.Contains(parts[1], ".") {
		return "", errors.New("invalid email")
	}
	return value, nil
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func importedCredentialDigest(raw, imported string, allowImported bool) (string, error) {
	if raw != "" && imported != "" {
		return "", errors.New("token and token digest are mutually exclusive")
	}
	if raw != "" {
		return digest(raw), nil
	}
	if !allowImported {
		return "", errors.New("token is required")
	}
	if len(imported) != sha256.Size*2 || strings.ToLower(imported) != imported {
		return "", errors.New("token digest must be lowercase SHA-256 hex")
	}
	decoded, err := hex.DecodeString(imported)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("token digest must be lowercase SHA-256 hex")
	}
	return imported, nil
}

func opaqueID(prefix, value string) string {
	sum := sha256.Sum256([]byte(value))
	return prefix + hex.EncodeToString(sum[:12])
}

func requestDigest(request any) (string, error) {
	raw, err := msgpack.Marshal(request)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func cachedResponse(ctx context.Context, tx *sql.Tx, requestID, command string, request, response any) (bool, error) {
	fingerprint, err := requestDigest(request)
	if err != nil {
		return false, err
	}
	var storedCommand, storedFingerprint string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT command_name,request_hash,response FROM auth_commands WHERE request_id=?`, requestID).
		Scan(&storedCommand, &storedFingerprint, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if storedCommand != command || storedFingerprint != fingerprint {
		return false, errors.New("request id was already used for a different command")
	}
	if err := msgpack.Unmarshal(raw, response); err != nil {
		return false, fmt.Errorf("decode command receipt: %w", err)
	}
	return true, nil
}

func saveResponse(ctx context.Context, tx *sql.Tx, requestID, command string, request, response any) error {
	fingerprint, err := requestDigest(request)
	if err != nil {
		return err
	}
	raw, err := msgpack.Marshal(response)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO auth_commands(request_id,command_name,request_hash,response) VALUES (?,?,?,?)`,
		requestID, command, fingerprint, raw)
	return err
}

func recordAudit(ctx context.Context, tx *sql.Tx, requestID, action, subjectType, subjectID, actorID, outcome string, at time.Time) error {
	eventID := opaqueID("audit_", requestID+"\x00"+action)
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO auth_audit_events(id,action,subject_type,subject_id,actor_id,outcome,occurred_at) VALUES (?,?,?,?,?,?,?)`,
		eventID, action, subjectType, emptyToNil(subjectID), emptyToNil(actorID), outcome, at.UTC().UnixNano())
	return err
}

func emptyToNil(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableNanos(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC().UnixNano()
}

func nanosTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	at := time.Unix(0, value.Int64).UTC()
	return &at
}

func validateWindow(start time.Time, end *time.Time) error {
	if err := requireTime("created time", start); err != nil {
		return err
	}
	if end != nil && !end.After(start) {
		return errors.New("expiry must be after creation")
	}
	return nil
}

func validScope(scope string) bool {
	return scope == "read" || scope == "write" || scope == "full"
}

func scopeAllows(actual, required string) bool {
	rank := map[string]int{"read": 1, "write": 2, "full": 3}
	return rank[actual] >= rank[required] && rank[required] != 0
}

func (o *Owner) ImportIdentity(ctx context.Context, request AdminIdentityImportRequest) (IdentityImportResult, error) {
	if err := validVersion(request.Version); err != nil {
		return IdentityImportResult{}, err
	}
	if err := require("request id", request.RequestID); err != nil {
		return IdentityImportResult{}, err
	}
	if err := require("identity id", request.IdentityID); err != nil {
		return IdentityImportResult{}, err
	}
	email, err := normalizedEmail(request.Email)
	if err != nil {
		return IdentityImportResult{}, err
	}
	if request.State != IdentityEnabled && request.State != IdentityDisabled {
		return IdentityImportResult{}, errors.New("identity state must be enabled or disabled")
	}
	if err := requireTime("imported at", request.ImportedAt); err != nil {
		return IdentityImportResult{}, err
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return IdentityImportResult{}, err
	}
	defer tx.Rollback()
	var result IdentityImportResult
	if ok, err := cachedResponse(ctx, tx, request.RequestID, FnAdminIdentityImport, request, &result); err != nil || ok {
		return result, err
	}

	var existingID, existingEmail string
	err = tx.QueryRowContext(ctx, `SELECT id,email FROM auth_identities WHERE id=? OR email=?`, request.IdentityID, email).
		Scan(&existingID, &existingEmail)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, `INSERT INTO auth_identities(id,email,email_hash,role,state,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`,
			request.IdentityID, email, digest(email), "admin", request.State, request.ImportedAt.UTC().UnixNano(), request.ImportedAt.UTC().UnixNano())
		result = IdentityImportResult{Version: ContractVersion, IdentityID: request.IdentityID, Imported: true}
	case err != nil:
		return IdentityImportResult{}, err
	case existingID != request.IdentityID || existingEmail != email:
		return IdentityImportResult{}, errors.New("identity id or email belongs to a different identity")
	default:
		_, err = tx.ExecContext(ctx, `UPDATE auth_identities SET state=?,updated_at=? WHERE id=?`,
			request.State, request.ImportedAt.UTC().UnixNano(), request.IdentityID)
		result = IdentityImportResult{Version: ContractVersion, IdentityID: request.IdentityID, Imported: true}
	}
	if err != nil {
		return IdentityImportResult{}, err
	}
	if err := recordAudit(ctx, tx, request.RequestID, "admin.identity.import", "identity", request.IdentityID, request.ActorID, "applied", request.ImportedAt); err != nil {
		return IdentityImportResult{}, err
	}
	if err := saveResponse(ctx, tx, request.RequestID, FnAdminIdentityImport, request, result); err != nil {
		return IdentityImportResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return IdentityImportResult{}, err
	}
	return result, nil
}

func (o *Owner) IssueMagicLink(ctx context.Context, request MagicLinkIssueRequest) (MagicLinkIssueResult, error) {
	if err := validVersion(request.Version); err != nil {
		return MagicLinkIssueResult{}, err
	}
	if err := require("request id", request.RequestID); err != nil {
		return MagicLinkIssueResult{}, err
	}
	if err := require("token", request.Token); err != nil {
		return MagicLinkIssueResult{}, err
	}
	email, err := normalizedEmail(request.Email)
	if err != nil {
		return MagicLinkIssueResult{}, err
	}
	if err := validateWindow(request.IssuedAt, &request.ExpiresAt); err != nil {
		return MagicLinkIssueResult{}, err
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return MagicLinkIssueResult{}, err
	}
	defer tx.Rollback()
	var result MagicLinkIssueResult
	if ok, err := cachedResponse(ctx, tx, request.RequestID, FnMagicLinkIssue, request, &result); err != nil || ok {
		return result, err
	}
	result = MagicLinkIssueResult{Version: ContractVersion, Accepted: true}
	var identityID, state string
	err = tx.QueryRowContext(ctx, `SELECT id,state FROM auth_identities WHERE email=?`, email).Scan(&identityID, &state)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return MagicLinkIssueResult{}, err
	}
	outcome := "ignored"
	if err == nil && state == string(IdentityEnabled) {
		result.ChallengeID = opaqueID("challenge_", request.RequestID)
		_, err = tx.ExecContext(ctx, `INSERT INTO auth_magic_link_challenges(id,identity_id,token_hash,issued_at,expires_at) VALUES (?,?,?,?,?)`,
			result.ChallengeID, identityID, digest(request.Token), request.IssuedAt.UTC().UnixNano(), request.ExpiresAt.UTC().UnixNano())
		if err != nil {
			return MagicLinkIssueResult{}, fmt.Errorf("store magic-link challenge: %w", err)
		}
		result.Deliver = true
		outcome = "issued"
	}
	if err := recordAudit(ctx, tx, request.RequestID, "magic-link.issue", "identity", identityID, "", outcome, request.IssuedAt); err != nil {
		return MagicLinkIssueResult{}, err
	}
	if err := saveResponse(ctx, tx, request.RequestID, FnMagicLinkIssue, request, result); err != nil {
		return MagicLinkIssueResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return MagicLinkIssueResult{}, err
	}
	return result, nil
}

func (o *Owner) ConsumeMagicLink(ctx context.Context, request MagicLinkConsumeRequest) (MagicLinkConsumeResult, error) {
	if err := validVersion(request.Version); err != nil {
		return MagicLinkConsumeResult{}, err
	}
	if err := require("request id", request.RequestID); err != nil {
		return MagicLinkConsumeResult{}, err
	}
	if err := require("token", request.Token); err != nil {
		return MagicLinkConsumeResult{}, err
	}
	if err := requireTime("consumed at", request.ConsumedAt); err != nil {
		return MagicLinkConsumeResult{}, err
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return MagicLinkConsumeResult{}, err
	}
	defer tx.Rollback()
	var result MagicLinkConsumeResult
	if ok, err := cachedResponse(ctx, tx, request.RequestID, FnMagicLinkConsume, request, &result); err != nil || ok {
		return result, err
	}
	result.Version = ContractVersion
	var challengeID, identityID, identityState string
	var expiresAt int64
	var consumedAt sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT c.id,c.identity_id,c.expires_at,c.consumed_at,i.state
		FROM auth_magic_link_challenges c JOIN auth_identities i ON i.id=c.identity_id WHERE c.token_hash=?`, digest(request.Token)).
		Scan(&challengeID, &identityID, &expiresAt, &consumedAt, &identityState)
	outcome := "invalid"
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return MagicLinkConsumeResult{}, err
	}
	if err == nil {
		result.ChallengeID = challengeID
		if !consumedAt.Valid && request.ConsumedAt.UTC().UnixNano() < expiresAt && identityState == string(IdentityEnabled) {
			updated, err := tx.ExecContext(ctx, `UPDATE auth_magic_link_challenges SET consumed_at=? WHERE id=? AND consumed_at IS NULL`,
				request.ConsumedAt.UTC().UnixNano(), challengeID)
			if err != nil {
				return MagicLinkConsumeResult{}, err
			}
			count, err := updated.RowsAffected()
			if err != nil {
				return MagicLinkConsumeResult{}, err
			}
			if count == 1 {
				result.Authenticated = true
				result.IdentityID = identityID
				outcome = "consumed"
			}
		} else if !consumedAt.Valid && request.ConsumedAt.UTC().UnixNano() >= expiresAt {
			if _, err := tx.ExecContext(ctx, `UPDATE auth_magic_link_challenges SET consumed_at=? WHERE id=? AND consumed_at IS NULL`,
				request.ConsumedAt.UTC().UnixNano(), challengeID); err != nil {
				return MagicLinkConsumeResult{}, err
			}
			outcome = "expired"
		}
	}
	if err := recordAudit(ctx, tx, request.RequestID, "magic-link.consume", "identity", identityID, "", outcome, request.ConsumedAt); err != nil {
		return MagicLinkConsumeResult{}, err
	}
	if err := saveResponse(ctx, tx, request.RequestID, FnMagicLinkConsume, request, result); err != nil {
		return MagicLinkConsumeResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return MagicLinkConsumeResult{}, err
	}
	return result, nil
}

func (o *Owner) CreateSession(ctx context.Context, request SessionCreateRequest) (SessionCreateResult, error) {
	if err := validVersion(request.Version); err != nil {
		return SessionCreateResult{}, err
	}
	for label, value := range map[string]string{
		"request id": request.RequestID, "challenge id": request.ChallengeID, "identity id": request.IdentityID, "token": request.Token,
	} {
		if err := require(label, value); err != nil {
			return SessionCreateResult{}, err
		}
	}
	if err := validateWindow(request.IssuedAt, &request.ExpiresAt); err != nil {
		return SessionCreateResult{}, err
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionCreateResult{}, err
	}
	defer tx.Rollback()
	var result SessionCreateResult
	if ok, err := cachedResponse(ctx, tx, request.RequestID, FnSessionCreate, request, &result); err != nil || ok {
		return result, err
	}
	var consumedAt sql.NullInt64
	var state string
	err = tx.QueryRowContext(ctx, `SELECT c.consumed_at,i.state FROM auth_magic_link_challenges c
		JOIN auth_identities i ON i.id=c.identity_id WHERE c.id=? AND c.identity_id=?`, request.ChallengeID, request.IdentityID).
		Scan(&consumedAt, &state)
	if errors.Is(err, sql.ErrNoRows) || !consumedAt.Valid || state != string(IdentityEnabled) {
		return SessionCreateResult{}, errors.New("consumed challenge is required")
	}
	if err != nil {
		return SessionCreateResult{}, err
	}
	result = SessionCreateResult{
		Version:   ContractVersion,
		SessionID: opaqueID("session_", request.RequestID),
		Created:   true,
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_sessions(id,identity_id,token_hash,issued_at,expires_at) VALUES (?,?,?,?,?)`,
		result.SessionID, request.IdentityID, digest(request.Token), request.IssuedAt.UTC().UnixNano(), request.ExpiresAt.UTC().UnixNano()); err != nil {
		return SessionCreateResult{}, fmt.Errorf("store session: %w", err)
	}
	if err := recordAudit(ctx, tx, request.RequestID, "session.create", "session", result.SessionID, request.IdentityID, "created", request.IssuedAt); err != nil {
		return SessionCreateResult{}, err
	}
	if err := saveResponse(ctx, tx, request.RequestID, FnSessionCreate, request, result); err != nil {
		return SessionCreateResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionCreateResult{}, err
	}
	return result, nil
}

func (o *Owner) ValidateSession(ctx context.Context, request SessionValidateRequest) (SessionValidateResult, error) {
	if err := validVersion(request.Version); err != nil {
		return SessionValidateResult{}, err
	}
	if err := require("token", request.Token); err != nil {
		return SessionValidateResult{}, err
	}
	if err := requireTime("validation time", request.At); err != nil {
		return SessionValidateResult{}, err
	}
	result := SessionValidateResult{Version: ContractVersion}
	err := o.store.db.QueryRowContext(ctx, `SELECT s.id,i.id,i.email,i.role FROM auth_sessions s
		JOIN auth_identities i ON i.id=s.identity_id
		WHERE s.token_hash=? AND s.revoked_at IS NULL AND s.expires_at>? AND i.state=?`,
		digest(request.Token), request.At.UTC().UnixNano(), IdentityEnabled).
		Scan(&result.SessionID, &result.IdentityID, &result.Email, &result.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return SessionValidateResult{}, err
	}
	result.Valid = true
	return result, nil
}

func (o *Owner) RevokeSession(ctx context.Context, request SessionRevokeRequest) (SessionRevokeResult, error) {
	if err := validVersion(request.Version); err != nil {
		return SessionRevokeResult{}, err
	}
	if err := require("request id", request.RequestID); err != nil {
		return SessionRevokeResult{}, err
	}
	if err := require("token", request.Token); err != nil {
		return SessionRevokeResult{}, err
	}
	if err := requireTime("revoked at", request.RevokedAt); err != nil {
		return SessionRevokeResult{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionRevokeResult{}, err
	}
	defer tx.Rollback()
	var result SessionRevokeResult
	if ok, err := cachedResponse(ctx, tx, request.RequestID, FnSessionRevoke, request, &result); err != nil || ok {
		return result, err
	}
	var sessionID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM auth_sessions WHERE token_hash=?`, digest(request.Token)).Scan(&sessionID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SessionRevokeResult{}, err
	}
	if err == nil {
		update, err := tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at=? WHERE id=? AND revoked_at IS NULL`,
			request.RevokedAt.UTC().UnixNano(), sessionID)
		if err != nil {
			return SessionRevokeResult{}, err
		}
		count, err := update.RowsAffected()
		if err != nil {
			return SessionRevokeResult{}, err
		}
		result.Revoked = count == 1
	}
	result.Version = ContractVersion
	if err := recordAudit(ctx, tx, request.RequestID, "session.revoke", "session", sessionID, request.ActorID, map[bool]string{true: "revoked", false: "not_found"}[result.Revoked], request.RevokedAt); err != nil {
		return SessionRevokeResult{}, err
	}
	if err := saveResponse(ctx, tx, request.RequestID, FnSessionRevoke, request, result); err != nil {
		return SessionRevokeResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionRevokeResult{}, err
	}
	return result, nil
}

func (o *Owner) IssueAPIToken(ctx context.Context, request APITokenIssueRequest) (APITokenMutationResult, error) {
	importRequest := APITokenImportRequest{
		Version: request.Version, RequestID: request.RequestID, TokenID: request.TokenID, Name: request.Name,
		Scope: request.Scope, Token: request.Token, ActorID: request.ActorID, CreatedAt: request.CreatedAt, ExpiresAt: request.ExpiresAt,
	}
	return o.storeAPIToken(ctx, FnAPITokenIssue, importRequest, false)
}

func (o *Owner) ImportAPIToken(ctx context.Context, request APITokenImportRequest) (APITokenMutationResult, error) {
	return o.storeAPIToken(ctx, FnAdminAPITokenImport, request, true)
}

func (o *Owner) storeAPIToken(ctx context.Context, command string, request APITokenImportRequest, allowImportedDigest bool) (APITokenMutationResult, error) {
	if err := validVersion(request.Version); err != nil {
		return APITokenMutationResult{}, err
	}
	for label, value := range map[string]string{
		"request id": request.RequestID, "token id": request.TokenID, "name": request.Name,
	} {
		if err := require(label, value); err != nil {
			return APITokenMutationResult{}, err
		}
	}
	if !validScope(request.Scope) {
		return APITokenMutationResult{}, errors.New("scope must be read, write, or full")
	}
	tokenDigest, err := importedCredentialDigest(request.Token, request.TokenDigest, allowImportedDigest)
	if err != nil {
		return APITokenMutationResult{}, err
	}
	if err := validateWindow(request.CreatedAt, request.ExpiresAt); err != nil {
		return APITokenMutationResult{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return APITokenMutationResult{}, err
	}
	defer tx.Rollback()
	var result APITokenMutationResult
	if ok, err := cachedResponse(ctx, tx, request.RequestID, command, request, &result); err != nil || ok {
		return result, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO auth_api_tokens(id,name,scope,token_hash,created_at,expires_at,revoked_at,last_used_at) VALUES (?,?,?,?,?,?,?,?)`,
		request.TokenID, strings.TrimSpace(request.Name), request.Scope, tokenDigest, request.CreatedAt.UTC().UnixNano(),
		nullableNanos(request.ExpiresAt), nullableNanos(request.RevokedAt), nullableNanos(request.LastUsedAt))
	if err != nil {
		return APITokenMutationResult{}, fmt.Errorf("store api token: %w", err)
	}
	result = APITokenMutationResult{Version: ContractVersion, TokenID: request.TokenID, Created: true}
	if err := recordAudit(ctx, tx, request.RequestID, strings.TrimPrefix(command, "credential-registry.v1."), "api-token", request.TokenID, request.ActorID, "created", request.CreatedAt); err != nil {
		return APITokenMutationResult{}, err
	}
	if err := saveResponse(ctx, tx, request.RequestID, command, request, result); err != nil {
		return APITokenMutationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return APITokenMutationResult{}, err
	}
	return result, nil
}

func (o *Owner) ValidateAPIToken(ctx context.Context, request APITokenValidateRequest) (APITokenValidateResult, error) {
	if err := validVersion(request.Version); err != nil {
		return APITokenValidateResult{}, err
	}
	if err := require("request id", request.RequestID); err != nil {
		return APITokenValidateResult{}, err
	}
	if err := require("token", request.Token); err != nil {
		return APITokenValidateResult{}, err
	}
	if !validScope(request.RequiredScope) {
		return APITokenValidateResult{}, errors.New("required scope must be read, write, or full")
	}
	if err := requireTime("validated at", request.ValidatedAt); err != nil {
		return APITokenValidateResult{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return APITokenValidateResult{}, err
	}
	defer tx.Rollback()
	var result APITokenValidateResult
	if ok, err := cachedResponse(ctx, tx, request.RequestID, FnAPITokenValidate, request, &result); err != nil || ok {
		return result, err
	}
	result.Version = ContractVersion
	var expiresAt sql.NullInt64
	var revokedAt sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT id,name,scope,expires_at,revoked_at FROM auth_api_tokens WHERE token_hash=?`, digest(request.Token)).
		Scan(&result.TokenID, &result.Name, &result.Scope, &expiresAt, &revokedAt)
	outcome := "invalid"
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return APITokenValidateResult{}, err
	}
	if err == nil && !revokedAt.Valid && (!expiresAt.Valid || expiresAt.Int64 > request.ValidatedAt.UTC().UnixNano()) &&
		scopeAllows(result.Scope, request.RequiredScope) {
		result.Valid = true
		outcome = "valid"
		if _, err := tx.ExecContext(ctx, `UPDATE auth_api_tokens SET last_used_at=? WHERE id=?`,
			request.ValidatedAt.UTC().UnixNano(), result.TokenID); err != nil {
			return APITokenValidateResult{}, err
		}
	} else if err != nil || !result.Valid {
		result.TokenID, result.Name, result.Scope = "", "", ""
	}
	if err := recordAudit(ctx, tx, request.RequestID, "api-token.validate", "api-token", result.TokenID, "", outcome, request.ValidatedAt); err != nil {
		return APITokenValidateResult{}, err
	}
	if err := saveResponse(ctx, tx, request.RequestID, FnAPITokenValidate, request, result); err != nil {
		return APITokenValidateResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return APITokenValidateResult{}, err
	}
	return result, nil
}

func (o *Owner) ListAPITokens(ctx context.Context, request APITokenListRequest) (APITokenListResult, error) {
	if err := validVersion(request.Version); err != nil {
		return APITokenListResult{}, err
	}
	query := `SELECT id,name,scope,created_at,expires_at,revoked_at,last_used_at FROM auth_api_tokens`
	if !request.IncludeRevoked {
		query += ` WHERE revoked_at IS NULL`
	}
	query += ` ORDER BY created_at,id`
	rows, err := o.store.db.QueryContext(ctx, query)
	if err != nil {
		return APITokenListResult{}, err
	}
	defer rows.Close()
	result := APITokenListResult{Version: ContractVersion, Tokens: []APITokenMetadata{}}
	for rows.Next() {
		var item APITokenMetadata
		var createdAt int64
		var expiresAt, revokedAt, lastUsedAt sql.NullInt64
		if err := rows.Scan(&item.TokenID, &item.Name, &item.Scope, &createdAt, &expiresAt, &revokedAt, &lastUsedAt); err != nil {
			return APITokenListResult{}, err
		}
		item.CreatedAt = time.Unix(0, createdAt).UTC()
		item.ExpiresAt = nanosTime(expiresAt)
		item.RevokedAt = nanosTime(revokedAt)
		item.LastUsedAt = nanosTime(lastUsedAt)
		result.Tokens = append(result.Tokens, item)
	}
	return result, rows.Err()
}

func (o *Owner) RevokeAPIToken(ctx context.Context, request APITokenRevokeRequest) (APITokenMutationResult, error) {
	if err := validVersion(request.Version); err != nil {
		return APITokenMutationResult{}, err
	}
	if err := require("request id", request.RequestID); err != nil {
		return APITokenMutationResult{}, err
	}
	if err := require("token id", request.TokenID); err != nil {
		return APITokenMutationResult{}, err
	}
	if err := requireTime("revoked at", request.RevokedAt); err != nil {
		return APITokenMutationResult{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return APITokenMutationResult{}, err
	}
	defer tx.Rollback()
	var result APITokenMutationResult
	if ok, err := cachedResponse(ctx, tx, request.RequestID, FnAdminAPITokenRevoke, request, &result); err != nil || ok {
		return result, err
	}
	update, err := tx.ExecContext(ctx, `UPDATE auth_api_tokens SET revoked_at=? WHERE id=? AND revoked_at IS NULL`,
		request.RevokedAt.UTC().UnixNano(), request.TokenID)
	if err != nil {
		return APITokenMutationResult{}, err
	}
	count, err := update.RowsAffected()
	if err != nil {
		return APITokenMutationResult{}, err
	}
	result = APITokenMutationResult{Version: ContractVersion, TokenID: request.TokenID, Revoked: count == 1}
	if err := recordAudit(ctx, tx, request.RequestID, "admin.api-token.revoke", "api-token", request.TokenID, request.ActorID, map[bool]string{true: "revoked", false: "not_found"}[result.Revoked], request.RevokedAt); err != nil {
		return APITokenMutationResult{}, err
	}
	if err := saveResponse(ctx, tx, request.RequestID, FnAdminAPITokenRevoke, request, result); err != nil {
		return APITokenMutationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return APITokenMutationResult{}, err
	}
	return result, nil
}

func (o *Owner) ImportSourceCredential(ctx context.Context, request SourceCredentialImportRequest) (SourceCredentialMutationResult, error) {
	return o.storeSourceCredential(ctx, FnAdminSourceImport, request)
}

func (o *Owner) storeSourceCredential(ctx context.Context, command string, request SourceCredentialImportRequest) (SourceCredentialMutationResult, error) {
	if err := validVersion(request.Version); err != nil {
		return SourceCredentialMutationResult{}, err
	}
	for label, value := range map[string]string{
		"request id": request.RequestID, "credential id": request.CredentialID, "source id": request.SourceID,
	} {
		if err := require(label, value); err != nil {
			return SourceCredentialMutationResult{}, err
		}
	}
	if err := validateWindow(request.CreatedAt, request.ExpiresAt); err != nil {
		return SourceCredentialMutationResult{}, err
	}
	tokenDigest, err := importedCredentialDigest(request.Token, request.TokenDigest, true)
	if err != nil {
		return SourceCredentialMutationResult{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return SourceCredentialMutationResult{}, err
	}
	defer tx.Rollback()
	var result SourceCredentialMutationResult
	if ok, err := cachedResponse(ctx, tx, request.RequestID, command, request, &result); err != nil || ok {
		return result, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO auth_source_credentials(id,source_id,token_hash,created_at,expires_at,revoked_at,last_used_at) VALUES (?,?,?,?,?,?,?)`,
		request.CredentialID, request.SourceID, tokenDigest, request.CreatedAt.UTC().UnixNano(),
		nullableNanos(request.ExpiresAt), nullableNanos(request.RevokedAt), nullableNanos(request.LastUsedAt))
	if err != nil {
		return SourceCredentialMutationResult{}, fmt.Errorf("store source credential: %w", err)
	}
	result = SourceCredentialMutationResult{Version: ContractVersion, CredentialID: request.CredentialID, SourceID: request.SourceID, Created: true}
	if err := recordAudit(ctx, tx, request.RequestID, strings.TrimPrefix(command, "credential-registry.v1."), "source-credential", request.CredentialID, request.ActorID, "created", request.CreatedAt); err != nil {
		return SourceCredentialMutationResult{}, err
	}
	if err := saveResponse(ctx, tx, request.RequestID, command, request, result); err != nil {
		return SourceCredentialMutationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SourceCredentialMutationResult{}, err
	}
	return result, nil
}

func (o *Owner) RotateSourceCredential(ctx context.Context, request SourceCredentialRotateRequest) (SourceCredentialMutationResult, error) {
	if err := validVersion(request.Version); err != nil {
		return SourceCredentialMutationResult{}, err
	}
	for label, value := range map[string]string{
		"request id": request.RequestID, "credential id": request.CredentialID, "source id": request.SourceID, "token": request.Token,
	} {
		if err := require(label, value); err != nil {
			return SourceCredentialMutationResult{}, err
		}
	}
	if err := validateWindow(request.RotatedAt, request.ExpiresAt); err != nil {
		return SourceCredentialMutationResult{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return SourceCredentialMutationResult{}, err
	}
	defer tx.Rollback()
	var result SourceCredentialMutationResult
	if ok, err := cachedResponse(ctx, tx, request.RequestID, FnAdminSourceRotate, request, &result); err != nil || ok {
		return result, err
	}
	if request.PreviousTokenID != "" {
		update, err := tx.ExecContext(ctx, `UPDATE auth_source_credentials SET revoked_at=? WHERE id=? AND source_id=? AND revoked_at IS NULL`,
			request.RotatedAt.UTC().UnixNano(), request.PreviousTokenID, request.SourceID)
		if err != nil {
			return SourceCredentialMutationResult{}, err
		}
		count, err := update.RowsAffected()
		if err != nil {
			return SourceCredentialMutationResult{}, err
		}
		if count != 1 {
			return SourceCredentialMutationResult{}, errors.New("previous source credential is not active")
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE auth_source_credentials SET revoked_at=? WHERE source_id=? AND revoked_at IS NULL`,
			request.RotatedAt.UTC().UnixNano(), request.SourceID); err != nil {
			return SourceCredentialMutationResult{}, err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO auth_source_credentials(id,source_id,token_hash,created_at,expires_at) VALUES (?,?,?,?,?)`,
		request.CredentialID, request.SourceID, digest(request.Token), request.RotatedAt.UTC().UnixNano(), nullableNanos(request.ExpiresAt))
	if err != nil {
		return SourceCredentialMutationResult{}, fmt.Errorf("store rotated source credential: %w", err)
	}
	result = SourceCredentialMutationResult{Version: ContractVersion, CredentialID: request.CredentialID, SourceID: request.SourceID, Created: true}
	if err := recordAudit(ctx, tx, request.RequestID, "admin.source-credential.rotate", "source-credential", request.CredentialID, request.ActorID, "rotated", request.RotatedAt); err != nil {
		return SourceCredentialMutationResult{}, err
	}
	if err := saveResponse(ctx, tx, request.RequestID, FnAdminSourceRotate, request, result); err != nil {
		return SourceCredentialMutationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SourceCredentialMutationResult{}, err
	}
	return result, nil
}

func (o *Owner) RevokeSourceCredential(ctx context.Context, request SourceCredentialRevokeRequest) (SourceCredentialMutationResult, error) {
	if err := validVersion(request.Version); err != nil {
		return SourceCredentialMutationResult{}, err
	}
	for label, value := range map[string]string{
		"request id": request.RequestID, "source id": request.SourceID,
	} {
		if err := require(label, value); err != nil {
			return SourceCredentialMutationResult{}, err
		}
	}
	if err := requireTime("revoked at", request.RevokedAt); err != nil {
		return SourceCredentialMutationResult{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return SourceCredentialMutationResult{}, err
	}
	defer tx.Rollback()
	var result SourceCredentialMutationResult
	if ok, err := cachedResponse(ctx, tx, request.RequestID, FnAdminSourceRevoke, request, &result); err != nil || ok {
		return result, err
	}
	var update sql.Result
	if request.CredentialID == "" {
		update, err = tx.ExecContext(ctx, `UPDATE auth_source_credentials SET revoked_at=? WHERE source_id=? AND revoked_at IS NULL`,
			request.RevokedAt.UTC().UnixNano(), request.SourceID)
	} else {
		update, err = tx.ExecContext(ctx, `UPDATE auth_source_credentials SET revoked_at=? WHERE id=? AND source_id=? AND revoked_at IS NULL`,
			request.RevokedAt.UTC().UnixNano(), request.CredentialID, request.SourceID)
	}
	if err != nil {
		return SourceCredentialMutationResult{}, err
	}
	count, err := update.RowsAffected()
	if err != nil {
		return SourceCredentialMutationResult{}, err
	}
	result = SourceCredentialMutationResult{
		Version: ContractVersion, CredentialID: request.CredentialID, SourceID: request.SourceID, Revoked: count > 0,
	}
	outcome := "not_found"
	if result.Revoked {
		outcome = "revoked"
	}
	if err := recordAudit(ctx, tx, request.RequestID, "admin.source-credential.revoke", "source-credential", request.CredentialID, request.ActorID, outcome, request.RevokedAt); err != nil {
		return SourceCredentialMutationResult{}, err
	}
	if err := saveResponse(ctx, tx, request.RequestID, FnAdminSourceRevoke, request, result); err != nil {
		return SourceCredentialMutationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SourceCredentialMutationResult{}, err
	}
	return result, nil
}

func (o *Owner) ValidateSourceCredential(ctx context.Context, request SourceCredentialValidateRequest) (SourceCredentialValidateResult, error) {
	if err := validVersion(request.Version); err != nil {
		return SourceCredentialValidateResult{}, err
	}
	if err := require("request id", request.RequestID); err != nil {
		return SourceCredentialValidateResult{}, err
	}
	if err := require("token", request.Token); err != nil {
		return SourceCredentialValidateResult{}, err
	}
	if err := requireTime("validated at", request.ValidatedAt); err != nil {
		return SourceCredentialValidateResult{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return SourceCredentialValidateResult{}, err
	}
	defer tx.Rollback()
	var result SourceCredentialValidateResult
	if ok, err := cachedResponse(ctx, tx, request.RequestID, FnSourceValidate, request, &result); err != nil || ok {
		return result, err
	}
	result.Version = ContractVersion
	var expiresAt, revokedAt sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT id,source_id,expires_at,revoked_at FROM auth_source_credentials WHERE token_hash=?`, digest(request.Token)).
		Scan(&result.CredentialID, &result.SourceID, &expiresAt, &revokedAt)
	outcome := "invalid"
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SourceCredentialValidateResult{}, err
	}
	if err == nil && !revokedAt.Valid && (!expiresAt.Valid || expiresAt.Int64 > request.ValidatedAt.UTC().UnixNano()) {
		result.Valid = true
		outcome = "valid"
		if _, err := tx.ExecContext(ctx, `UPDATE auth_source_credentials SET last_used_at=? WHERE id=?`,
			request.ValidatedAt.UTC().UnixNano(), result.CredentialID); err != nil {
			return SourceCredentialValidateResult{}, err
		}
	} else {
		result.CredentialID, result.SourceID = "", ""
	}
	if err := recordAudit(ctx, tx, request.RequestID, "source-credential.validate", "source-credential", result.CredentialID, "", outcome, request.ValidatedAt); err != nil {
		return SourceCredentialValidateResult{}, err
	}
	if err := saveResponse(ctx, tx, request.RequestID, FnSourceValidate, request, result); err != nil {
		return SourceCredentialValidateResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SourceCredentialValidateResult{}, err
	}
	return result, nil
}

func (o *Owner) Projection(ctx context.Context, request ProjectionRequest) (Projection, error) {
	if err := validVersion(request.Version); err != nil {
		return Projection{}, err
	}
	if err := requireTime("projection time", request.At); err != nil {
		return Projection{}, err
	}
	at := request.At.UTC().UnixNano()
	result := Projection{Version: ContractVersion}
	for _, item := range []struct {
		query string
		args  []any
		dest  *int
	}{
		{`SELECT COUNT(*) FROM auth_identities WHERE state=?`, []any{IdentityEnabled}, &result.EnabledIdentityCount},
		{`SELECT COUNT(*) FROM auth_magic_link_challenges WHERE consumed_at IS NULL AND expires_at>?`, []any{at}, &result.OpenChallengeCount},
		{`SELECT COUNT(*) FROM auth_sessions s JOIN auth_identities i ON i.id=s.identity_id
			WHERE s.revoked_at IS NULL AND s.expires_at>? AND i.state=?`, []any{at, IdentityEnabled}, &result.ActiveSessionCount},
		{`SELECT COUNT(*) FROM auth_api_tokens WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at>?)`, []any{at}, &result.ActiveAPITokenCount},
		{`SELECT COUNT(*) FROM auth_source_credentials WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at>?)`, []any{at}, &result.ActiveSourceCredentialCount},
	} {
		if err := o.store.db.QueryRowContext(ctx, item.query, item.args...).Scan(item.dest); err != nil {
			return Projection{}, err
		}
	}
	return result, nil
}

func (o *Owner) QueryAudit(ctx context.Context, request AdminAuditQueryRequest) (AdminAuditQueryResult, error) {
	if err := validVersion(request.Version); err != nil {
		return AdminAuditQueryResult{}, err
	}
	if request.Limit < 1 || request.Limit > 500 {
		return AdminAuditQueryResult{}, errors.New("audit limit must be 1..500")
	}
	rows, err := o.store.db.QueryContext(ctx, `SELECT id,action,subject_type,subject_id,actor_id,outcome,occurred_at
		FROM auth_audit_events ORDER BY occurred_at DESC,id DESC LIMIT ?`, request.Limit)
	if err != nil {
		return AdminAuditQueryResult{}, err
	}
	defer rows.Close()
	result := AdminAuditQueryResult{Version: ContractVersion, Events: []AuditEvent{}}
	for rows.Next() {
		var item AuditEvent
		var subjectID, actorID sql.NullString
		var occurredAt int64
		if err := rows.Scan(&item.EventID, &item.Action, &item.SubjectType, &subjectID, &actorID, &item.Outcome, &occurredAt); err != nil {
			return AdminAuditQueryResult{}, err
		}
		item.SubjectID, item.ActorID = subjectID.String, actorID.String
		item.OccurredAt = time.Unix(0, occurredAt).UTC()
		result.Events = append(result.Events, item)
	}
	return result, rows.Err()
}
