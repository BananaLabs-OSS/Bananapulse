package main

import "time"

// ContractVersion changes only for a deliberate wire incompatibility.
const ContractVersion = "credential-registry/v1"

const (
	FnCatalog             = "credential-registry.v1.catalog"
	FnAdminIdentityImport = "credential-registry.v1.admin.identity.import"
	FnMagicLinkIssue      = "credential-registry.v1.magic-link.issue"
	FnMagicLinkConsume    = "credential-registry.v1.magic-link.consume"
	FnSessionCreate       = "credential-registry.v1.session.create"
	FnSessionValidate     = "credential-registry.v1.session.validate"
	FnSessionRevoke       = "credential-registry.v1.session.revoke"
	FnAPITokenIssue       = "credential-registry.v1.api-token.issue"
	FnAPITokenValidate    = "credential-registry.v1.api-token.validate"
	FnAdminAPITokenImport = "credential-registry.v1.admin.api-token.import"
	FnAdminAPITokenList   = "credential-registry.v1.admin.api-token.list"
	FnAdminAPITokenRevoke = "credential-registry.v1.admin.api-token.revoke"
	FnAdminSourceImport   = "credential-registry.v1.admin.source-credential.import"
	FnAdminSourceRotate   = "credential-registry.v1.admin.source-credential.rotate"
	FnAdminSourceRevoke   = "credential-registry.v1.admin.source-credential.revoke"
	FnSourceValidate      = "credential-registry.v1.source-credential.validate"
	FnProjection          = "credential-registry.v1.projection"
	FnAdminAuditQuery     = "credential-registry.v1.admin.audit.query"
)

type Catalog struct {
	Version    string   `msgpack:"version" json:"version"`
	Commands   []string `msgpack:"commands" json:"commands"`
	Queries    []string `msgpack:"queries" json:"queries"`
	SecretRule string   `msgpack:"secret_rule" json:"secret_rule"`
}

type IdentityState string

const (
	IdentityEnabled  IdentityState = "enabled"
	IdentityDisabled IdentityState = "disabled"
)

type AdminIdentityImportRequest struct {
	Version    string        `msgpack:"version" json:"version"`
	RequestID  string        `msgpack:"request_id" json:"request_id"`
	IdentityID string        `msgpack:"identity_id" json:"identity_id"`
	Email      string        `msgpack:"email" json:"email"`
	State      IdentityState `msgpack:"state" json:"state"`
	ActorID    string        `msgpack:"actor_id,omitempty" json:"actor_id,omitempty"`
	ImportedAt time.Time     `msgpack:"imported_at" json:"imported_at"`
}

type IdentityImportResult struct {
	Version    string `msgpack:"version" json:"version"`
	IdentityID string `msgpack:"identity_id" json:"identity_id"`
	Imported   bool   `msgpack:"imported" json:"imported"`
}

// MagicLinkIssueRequest contains a caller-generated opaque Token. The owner
// persists only its SHA-256 digest. The caller already possesses the plaintext
// needed to construct a host-owned email and it is never returned by the owner.
type MagicLinkIssueRequest struct {
	Version   string    `msgpack:"version" json:"version"`
	RequestID string    `msgpack:"request_id" json:"request_id"`
	Email     string    `msgpack:"email" json:"email"`
	Token     string    `msgpack:"token" json:"token"`
	IssuedAt  time.Time `msgpack:"issued_at" json:"issued_at"`
	ExpiresAt time.Time `msgpack:"expires_at" json:"expires_at"`
}

type MagicLinkIssueResult struct {
	Version     string `msgpack:"version" json:"version"`
	Accepted    bool   `msgpack:"accepted" json:"accepted"`
	Deliver     bool   `msgpack:"deliver" json:"deliver"`
	ChallengeID string `msgpack:"challenge_id,omitempty" json:"challenge_id,omitempty"`
}

type MagicLinkConsumeRequest struct {
	Version    string    `msgpack:"version" json:"version"`
	RequestID  string    `msgpack:"request_id" json:"request_id"`
	Token      string    `msgpack:"token" json:"token"`
	ConsumedAt time.Time `msgpack:"consumed_at" json:"consumed_at"`
}

type MagicLinkConsumeResult struct {
	Version       string `msgpack:"version" json:"version"`
	Authenticated bool   `msgpack:"authenticated" json:"authenticated"`
	ChallengeID   string `msgpack:"challenge_id,omitempty" json:"challenge_id,omitempty"`
	IdentityID    string `msgpack:"identity_id,omitempty" json:"identity_id,omitempty"`
}

type SessionCreateRequest struct {
	Version     string    `msgpack:"version" json:"version"`
	RequestID   string    `msgpack:"request_id" json:"request_id"`
	ChallengeID string    `msgpack:"challenge_id" json:"challenge_id"`
	IdentityID  string    `msgpack:"identity_id" json:"identity_id"`
	Token       string    `msgpack:"token" json:"token"`
	IssuedAt    time.Time `msgpack:"issued_at" json:"issued_at"`
	ExpiresAt   time.Time `msgpack:"expires_at" json:"expires_at"`
}

type SessionCreateResult struct {
	Version   string `msgpack:"version" json:"version"`
	SessionID string `msgpack:"session_id" json:"session_id"`
	Created   bool   `msgpack:"created" json:"created"`
}

type SessionValidateRequest struct {
	Version string    `msgpack:"version" json:"version"`
	Token   string    `msgpack:"token" json:"token"`
	At      time.Time `msgpack:"at" json:"at"`
}

type SessionValidateResult struct {
	Version    string `msgpack:"version" json:"version"`
	Valid      bool   `msgpack:"valid" json:"valid"`
	SessionID  string `msgpack:"session_id,omitempty" json:"session_id,omitempty"`
	IdentityID string `msgpack:"identity_id,omitempty" json:"identity_id,omitempty"`
	Email      string `msgpack:"email,omitempty" json:"email,omitempty"`
	Role       string `msgpack:"role,omitempty" json:"role,omitempty"`
}

type SessionRevokeRequest struct {
	Version   string    `msgpack:"version" json:"version"`
	RequestID string    `msgpack:"request_id" json:"request_id"`
	Token     string    `msgpack:"token" json:"token"`
	ActorID   string    `msgpack:"actor_id,omitempty" json:"actor_id,omitempty"`
	RevokedAt time.Time `msgpack:"revoked_at" json:"revoked_at"`
}

type SessionRevokeResult struct {
	Version string `msgpack:"version" json:"version"`
	Revoked bool   `msgpack:"revoked" json:"revoked"`
}

type APITokenIssueRequest struct {
	Version   string     `msgpack:"version" json:"version"`
	RequestID string     `msgpack:"request_id" json:"request_id"`
	TokenID   string     `msgpack:"token_id" json:"token_id"`
	Name      string     `msgpack:"name" json:"name"`
	Scope     string     `msgpack:"scope" json:"scope"`
	Token     string     `msgpack:"token" json:"token"`
	ActorID   string     `msgpack:"actor_id,omitempty" json:"actor_id,omitempty"`
	CreatedAt time.Time  `msgpack:"created_at" json:"created_at"`
	ExpiresAt *time.Time `msgpack:"expires_at,omitempty" json:"expires_at,omitempty"`
}

type APITokenImportRequest struct {
	Version     string     `msgpack:"version" json:"version"`
	RequestID   string     `msgpack:"request_id" json:"request_id"`
	TokenID     string     `msgpack:"token_id" json:"token_id"`
	Name        string     `msgpack:"name" json:"name"`
	Scope       string     `msgpack:"scope" json:"scope"`
	Token       string     `msgpack:"token,omitempty" json:"token,omitempty"`
	TokenDigest string     `msgpack:"token_digest,omitempty" json:"token_digest,omitempty"`
	ActorID     string     `msgpack:"actor_id,omitempty" json:"actor_id,omitempty"`
	CreatedAt   time.Time  `msgpack:"created_at" json:"created_at"`
	ExpiresAt   *time.Time `msgpack:"expires_at,omitempty" json:"expires_at,omitempty"`
	RevokedAt   *time.Time `msgpack:"revoked_at,omitempty" json:"revoked_at,omitempty"`
	LastUsedAt  *time.Time `msgpack:"last_used_at,omitempty" json:"last_used_at,omitempty"`
}

type APITokenMutationResult struct {
	Version string `msgpack:"version" json:"version"`
	TokenID string `msgpack:"token_id" json:"token_id"`
	Created bool   `msgpack:"created,omitempty" json:"created,omitempty"`
	Revoked bool   `msgpack:"revoked,omitempty" json:"revoked,omitempty"`
}

type APITokenValidateRequest struct {
	Version       string    `msgpack:"version" json:"version"`
	RequestID     string    `msgpack:"request_id" json:"request_id"`
	Token         string    `msgpack:"token" json:"token"`
	RequiredScope string    `msgpack:"required_scope" json:"required_scope"`
	ValidatedAt   time.Time `msgpack:"validated_at" json:"validated_at"`
}

type APITokenValidateResult struct {
	Version string `msgpack:"version" json:"version"`
	Valid   bool   `msgpack:"valid" json:"valid"`
	TokenID string `msgpack:"token_id,omitempty" json:"token_id,omitempty"`
	Name    string `msgpack:"name,omitempty" json:"name,omitempty"`
	Scope   string `msgpack:"scope,omitempty" json:"scope,omitempty"`
}

type APITokenListRequest struct {
	Version        string `msgpack:"version" json:"version"`
	IncludeRevoked bool   `msgpack:"include_revoked,omitempty" json:"include_revoked,omitempty"`
}

type APITokenMetadata struct {
	TokenID    string     `msgpack:"token_id" json:"token_id"`
	Name       string     `msgpack:"name" json:"name"`
	Scope      string     `msgpack:"scope" json:"scope"`
	CreatedAt  time.Time  `msgpack:"created_at" json:"created_at"`
	ExpiresAt  *time.Time `msgpack:"expires_at,omitempty" json:"expires_at,omitempty"`
	RevokedAt  *time.Time `msgpack:"revoked_at,omitempty" json:"revoked_at,omitempty"`
	LastUsedAt *time.Time `msgpack:"last_used_at,omitempty" json:"last_used_at,omitempty"`
}

type APITokenListResult struct {
	Version string             `msgpack:"version" json:"version"`
	Tokens  []APITokenMetadata `msgpack:"tokens" json:"tokens"`
}

type APITokenRevokeRequest struct {
	Version   string    `msgpack:"version" json:"version"`
	RequestID string    `msgpack:"request_id" json:"request_id"`
	TokenID   string    `msgpack:"token_id" json:"token_id"`
	ActorID   string    `msgpack:"actor_id,omitempty" json:"actor_id,omitempty"`
	RevokedAt time.Time `msgpack:"revoked_at" json:"revoked_at"`
}

type SourceCredentialImportRequest struct {
	Version      string     `msgpack:"version" json:"version"`
	RequestID    string     `msgpack:"request_id" json:"request_id"`
	CredentialID string     `msgpack:"credential_id" json:"credential_id"`
	SourceID     string     `msgpack:"source_id" json:"source_id"`
	Token        string     `msgpack:"token,omitempty" json:"token,omitempty"`
	TokenDigest  string     `msgpack:"token_digest,omitempty" json:"token_digest,omitempty"`
	ActorID      string     `msgpack:"actor_id,omitempty" json:"actor_id,omitempty"`
	CreatedAt    time.Time  `msgpack:"created_at" json:"created_at"`
	ExpiresAt    *time.Time `msgpack:"expires_at,omitempty" json:"expires_at,omitempty"`
	RevokedAt    *time.Time `msgpack:"revoked_at,omitempty" json:"revoked_at,omitempty"`
	LastUsedAt   *time.Time `msgpack:"last_used_at,omitempty" json:"last_used_at,omitempty"`
}

type SourceCredentialRotateRequest struct {
	Version         string     `msgpack:"version" json:"version"`
	RequestID       string     `msgpack:"request_id" json:"request_id"`
	PreviousTokenID string     `msgpack:"previous_credential_id,omitempty" json:"previous_credential_id,omitempty"`
	CredentialID    string     `msgpack:"credential_id" json:"credential_id"`
	SourceID        string     `msgpack:"source_id" json:"source_id"`
	Token           string     `msgpack:"token" json:"token"`
	ActorID         string     `msgpack:"actor_id,omitempty" json:"actor_id,omitempty"`
	RotatedAt       time.Time  `msgpack:"rotated_at" json:"rotated_at"`
	ExpiresAt       *time.Time `msgpack:"expires_at,omitempty" json:"expires_at,omitempty"`
}

type SourceCredentialMutationResult struct {
	Version      string `msgpack:"version" json:"version"`
	CredentialID string `msgpack:"credential_id" json:"credential_id"`
	SourceID     string `msgpack:"source_id" json:"source_id"`
	Created      bool   `msgpack:"created,omitempty" json:"created,omitempty"`
	Revoked      bool   `msgpack:"revoked,omitempty" json:"revoked,omitempty"`
}

type SourceCredentialRevokeRequest struct {
	Version      string    `msgpack:"version" json:"version"`
	RequestID    string    `msgpack:"request_id" json:"request_id"`
	CredentialID string    `msgpack:"credential_id,omitempty" json:"credential_id,omitempty"`
	SourceID     string    `msgpack:"source_id" json:"source_id"`
	ActorID      string    `msgpack:"actor_id,omitempty" json:"actor_id,omitempty"`
	RevokedAt    time.Time `msgpack:"revoked_at" json:"revoked_at"`
}

type SourceCredentialValidateRequest struct {
	Version     string    `msgpack:"version" json:"version"`
	RequestID   string    `msgpack:"request_id" json:"request_id"`
	Token       string    `msgpack:"token" json:"token"`
	ValidatedAt time.Time `msgpack:"validated_at" json:"validated_at"`
}

type SourceCredentialValidateResult struct {
	Version      string `msgpack:"version" json:"version"`
	Valid        bool   `msgpack:"valid" json:"valid"`
	CredentialID string `msgpack:"credential_id,omitempty" json:"credential_id,omitempty"`
	SourceID     string `msgpack:"source_id,omitempty" json:"source_id,omitempty"`
}

type ProjectionRequest struct {
	Version string    `msgpack:"version" json:"version"`
	At      time.Time `msgpack:"at" json:"at"`
}

// Projection deliberately excludes email addresses, credential digests,
// identifiers, and audit details.
type Projection struct {
	Version                     string `msgpack:"version" json:"version"`
	EnabledIdentityCount        int    `msgpack:"enabled_identity_count" json:"enabled_identity_count"`
	OpenChallengeCount          int    `msgpack:"open_challenge_count" json:"open_challenge_count"`
	ActiveSessionCount          int    `msgpack:"active_session_count" json:"active_session_count"`
	ActiveAPITokenCount         int    `msgpack:"active_api_token_count" json:"active_api_token_count"`
	ActiveSourceCredentialCount int    `msgpack:"active_source_credential_count" json:"active_source_credential_count"`
}

type AdminAuditQueryRequest struct {
	Version string `msgpack:"version" json:"version"`
	Limit   int    `msgpack:"limit" json:"limit"`
}

type AuditEvent struct {
	EventID     string    `msgpack:"event_id" json:"event_id"`
	Action      string    `msgpack:"action" json:"action"`
	SubjectType string    `msgpack:"subject_type" json:"subject_type"`
	SubjectID   string    `msgpack:"subject_id,omitempty" json:"subject_id,omitempty"`
	ActorID     string    `msgpack:"actor_id,omitempty" json:"actor_id,omitempty"`
	Outcome     string    `msgpack:"outcome" json:"outcome"`
	OccurredAt  time.Time `msgpack:"occurred_at" json:"occurred_at"`
}

type AdminAuditQueryResult struct {
	Version string       `msgpack:"version" json:"version"`
	Events  []AuditEvent `msgpack:"events" json:"events"`
}
