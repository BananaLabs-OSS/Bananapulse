package main

import "time"

const authOwnerCell = "credential-registry"

const (
	eventHostAuthAdminIdentityImport = "bananapulse.host.auth.admin.identity.import.v1"
	eventHostAuthMagicLinkIssue      = "bananapulse.host.auth.magic-link.issue.v1"
	eventHostAuthMagicLinkConsume    = "bananapulse.host.auth.magic-link.consume.v1"
	eventHostAuthSessionCreate       = "bananapulse.host.auth.session.create.v1"
	eventHostAuthSessionValidate     = "bananapulse.host.auth.session.validate.v1"
	eventHostAuthSessionRevoke       = "bananapulse.host.auth.session.revoke.v1"
	eventHostAuthAPITokenIssue       = "bananapulse.host.auth.api-token.issue.v1"
	eventHostAuthAPITokenValidate    = "bananapulse.host.auth.api-token.validate.v1"
	eventHostAuthAPITokenAdminImport = "bananapulse.host.auth.api-token.admin.import.v1"
	eventHostAuthAPITokenAdminList   = "bananapulse.host.auth.api-token.admin.list.v1"
	eventHostAuthAPITokenAdminRevoke = "bananapulse.host.auth.api-token.admin.revoke.v1"
	eventHostAuthSourceAdminImport   = "bananapulse.host.auth.source-credential.admin.import.v1"
	eventHostAuthSourceAdminRotate   = "bananapulse.host.auth.source-credential.admin.rotate.v1"
	eventHostAuthSourceAdminRevoke   = "bananapulse.host.auth.source-credential.admin.revoke.v1"
	eventHostAuthSourceValidate      = "bananapulse.host.auth.source-credential.validate.v1"
	eventHostAuthProjection          = "bananapulse.host.auth.projection.v1"
	eventHostAuthAdminAuditQuery     = "bananapulse.host.auth.admin.audit.query.v1"
)

const (
	providerAuthAdminIdentityImport = "credential-registry.v1.admin.identity.import"
	providerAuthMagicLinkIssue      = "credential-registry.v1.magic-link.issue"
	providerAuthMagicLinkConsume    = "credential-registry.v1.magic-link.consume"
	providerAuthSessionCreate       = "credential-registry.v1.session.create"
	providerAuthSessionValidate     = "credential-registry.v1.session.validate"
	providerAuthSessionRevoke       = "credential-registry.v1.session.revoke"
	providerAuthAPITokenIssue       = "credential-registry.v1.api-token.issue"
	providerAuthAPITokenValidate    = "credential-registry.v1.api-token.validate"
	providerAuthAPITokenAdminImport = "credential-registry.v1.admin.api-token.import"
	providerAuthAPITokenAdminList   = "credential-registry.v1.admin.api-token.list"
	providerAuthAPITokenAdminRevoke = "credential-registry.v1.admin.api-token.revoke"
	providerAuthSourceAdminImport   = "credential-registry.v1.admin.source-credential.import"
	providerAuthSourceAdminRotate   = "credential-registry.v1.admin.source-credential.rotate"
	providerAuthSourceAdminRevoke   = "credential-registry.v1.admin.source-credential.revoke"
	providerAuthSourceValidate      = "credential-registry.v1.source-credential.validate"
	providerAuthProjection          = "credential-registry.v1.projection"
	providerAuthAdminAuditQuery     = "credential-registry.v1.admin.audit.query"
)

func authBridgeRequest(event string) (string, any, bool) {
	switch event {
	case eventHostAuthAdminIdentityImport:
		return providerAuthAdminIdentityImport, &bridgeAuthAdminIdentityImportRequest{}, true
	case eventHostAuthMagicLinkIssue:
		return providerAuthMagicLinkIssue, &bridgeAuthMagicLinkIssueRequest{}, true
	case eventHostAuthMagicLinkConsume:
		return providerAuthMagicLinkConsume, &bridgeAuthMagicLinkConsumeRequest{}, true
	case eventHostAuthSessionCreate:
		return providerAuthSessionCreate, &bridgeAuthSessionCreateRequest{}, true
	case eventHostAuthSessionValidate:
		return providerAuthSessionValidate, &bridgeAuthSessionValidateRequest{}, true
	case eventHostAuthSessionRevoke:
		return providerAuthSessionRevoke, &bridgeAuthSessionRevokeRequest{}, true
	case eventHostAuthAPITokenIssue:
		return providerAuthAPITokenIssue, &bridgeAuthAPITokenIssueRequest{}, true
	case eventHostAuthAPITokenValidate:
		return providerAuthAPITokenValidate, &bridgeAuthAPITokenValidateRequest{}, true
	case eventHostAuthAPITokenAdminImport:
		return providerAuthAPITokenAdminImport, &bridgeAuthAPITokenImportRequest{}, true
	case eventHostAuthAPITokenAdminList:
		return providerAuthAPITokenAdminList, &bridgeAuthAPITokenListRequest{}, true
	case eventHostAuthAPITokenAdminRevoke:
		return providerAuthAPITokenAdminRevoke, &bridgeAuthAPITokenRevokeRequest{}, true
	case eventHostAuthSourceAdminImport:
		return providerAuthSourceAdminImport, &bridgeAuthSourceCredentialImportRequest{}, true
	case eventHostAuthSourceAdminRotate:
		return providerAuthSourceAdminRotate, &bridgeAuthSourceCredentialRotateRequest{}, true
	case eventHostAuthSourceAdminRevoke:
		return providerAuthSourceAdminRevoke, &bridgeAuthSourceCredentialRevokeRequest{}, true
	case eventHostAuthSourceValidate:
		return providerAuthSourceValidate, &bridgeAuthSourceCredentialValidateRequest{}, true
	case eventHostAuthProjection:
		return providerAuthProjection, &bridgeAuthProjectionRequest{}, true
	case eventHostAuthAdminAuditQuery:
		return providerAuthAdminAuditQuery, &bridgeAuthAdminAuditQueryRequest{}, true
	default:
		return "", nil, false
	}
}

func authAdminEvent(event string) bool {
	switch event {
	case eventHostAuthAdminIdentityImport,
		eventHostAuthAPITokenIssue,
		eventHostAuthAPITokenAdminImport,
		eventHostAuthAPITokenAdminList,
		eventHostAuthAPITokenAdminRevoke,
		eventHostAuthSourceAdminImport,
		eventHostAuthSourceAdminRotate,
		eventHostAuthSourceAdminRevoke,
		eventHostAuthAdminAuditQuery:
		return true
	default:
		return false
	}
}

type bridgeAuthAdminIdentityImportRequest struct {
	Version    string    `msgpack:"version" json:"version"`
	RequestID  string    `msgpack:"request_id" json:"request_id"`
	IdentityID string    `msgpack:"identity_id" json:"identity_id"`
	Email      string    `msgpack:"email" json:"email"`
	State      string    `msgpack:"state" json:"state"`
	ActorID    string    `msgpack:"actor_id,omitempty" json:"actor_id,omitempty"`
	ImportedAt time.Time `msgpack:"imported_at" json:"imported_at"`
}

type bridgeAuthMagicLinkIssueRequest struct {
	Version   string    `msgpack:"version" json:"version"`
	RequestID string    `msgpack:"request_id" json:"request_id"`
	Email     string    `msgpack:"email" json:"email"`
	Token     string    `msgpack:"token" json:"token"`
	IssuedAt  time.Time `msgpack:"issued_at" json:"issued_at"`
	ExpiresAt time.Time `msgpack:"expires_at" json:"expires_at"`
}

type bridgeAuthMagicLinkConsumeRequest struct {
	Version    string    `msgpack:"version" json:"version"`
	RequestID  string    `msgpack:"request_id" json:"request_id"`
	Token      string    `msgpack:"token" json:"token"`
	ConsumedAt time.Time `msgpack:"consumed_at" json:"consumed_at"`
}

type bridgeAuthSessionCreateRequest struct {
	Version     string    `msgpack:"version" json:"version"`
	RequestID   string    `msgpack:"request_id" json:"request_id"`
	ChallengeID string    `msgpack:"challenge_id" json:"challenge_id"`
	IdentityID  string    `msgpack:"identity_id" json:"identity_id"`
	Token       string    `msgpack:"token" json:"token"`
	IssuedAt    time.Time `msgpack:"issued_at" json:"issued_at"`
	ExpiresAt   time.Time `msgpack:"expires_at" json:"expires_at"`
}

type bridgeAuthSessionValidateRequest struct {
	Version string    `msgpack:"version" json:"version"`
	Token   string    `msgpack:"token" json:"token"`
	At      time.Time `msgpack:"at" json:"at"`
}

type bridgeAuthSessionRevokeRequest struct {
	Version   string    `msgpack:"version" json:"version"`
	RequestID string    `msgpack:"request_id" json:"request_id"`
	Token     string    `msgpack:"token" json:"token"`
	ActorID   string    `msgpack:"actor_id,omitempty" json:"actor_id,omitempty"`
	RevokedAt time.Time `msgpack:"revoked_at" json:"revoked_at"`
}

type bridgeAuthAPITokenIssueRequest struct {
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

type bridgeAuthAPITokenImportRequest struct {
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

type bridgeAuthAPITokenValidateRequest struct {
	Version       string    `msgpack:"version" json:"version"`
	RequestID     string    `msgpack:"request_id" json:"request_id"`
	Token         string    `msgpack:"token" json:"token"`
	RequiredScope string    `msgpack:"required_scope" json:"required_scope"`
	ValidatedAt   time.Time `msgpack:"validated_at" json:"validated_at"`
}

type bridgeAuthAPITokenListRequest struct {
	Version        string `msgpack:"version" json:"version"`
	IncludeRevoked bool   `msgpack:"include_revoked,omitempty" json:"include_revoked,omitempty"`
}

type bridgeAuthAPITokenRevokeRequest struct {
	Version   string    `msgpack:"version" json:"version"`
	RequestID string    `msgpack:"request_id" json:"request_id"`
	TokenID   string    `msgpack:"token_id" json:"token_id"`
	ActorID   string    `msgpack:"actor_id,omitempty" json:"actor_id,omitempty"`
	RevokedAt time.Time `msgpack:"revoked_at" json:"revoked_at"`
}

type bridgeAuthSourceCredentialImportRequest struct {
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

type bridgeAuthSourceCredentialRotateRequest struct {
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

type bridgeAuthSourceCredentialRevokeRequest struct {
	Version      string    `msgpack:"version" json:"version"`
	RequestID    string    `msgpack:"request_id" json:"request_id"`
	CredentialID string    `msgpack:"credential_id,omitempty" json:"credential_id,omitempty"`
	SourceID     string    `msgpack:"source_id" json:"source_id"`
	ActorID      string    `msgpack:"actor_id,omitempty" json:"actor_id,omitempty"`
	RevokedAt    time.Time `msgpack:"revoked_at" json:"revoked_at"`
}

type bridgeAuthSourceCredentialValidateRequest struct {
	Version     string    `msgpack:"version" json:"version"`
	RequestID   string    `msgpack:"request_id" json:"request_id"`
	Token       string    `msgpack:"token" json:"token"`
	ValidatedAt time.Time `msgpack:"validated_at" json:"validated_at"`
}

type bridgeAuthProjectionRequest struct {
	Version string    `msgpack:"version" json:"version"`
	At      time.Time `msgpack:"at" json:"at"`
}

type bridgeAuthAdminAuditQueryRequest struct {
	Version string `msgpack:"version" json:"version"`
	Limit   int    `msgpack:"limit" json:"limit"`
}
