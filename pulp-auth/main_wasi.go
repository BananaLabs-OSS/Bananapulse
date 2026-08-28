//go:build wasip1

package main

import (
	"context"
	"fmt"

	"github.com/BananaLabs-OSS/Fiber/pulp"
	"github.com/vmihailenco/msgpack/v5"
)

func main() {}

func init() { pulp.OnInit(bootstrap) }

func bootstrap(_ []byte) error {
	store, err := openPulpStore()
	if err != nil {
		return fmt.Errorf("open auth store: %w", err)
	}
	owner, err := OpenOwner(store)
	if err != nil {
		return err
	}
	pulp.Provide(FnCatalog, func([]byte) ([]byte, error) {
		return msgpack.Marshal(Catalog{
			Version: ContractVersion,
			Commands: []string{
				FnAdminIdentityImport,
				FnMagicLinkIssue,
				FnMagicLinkConsume,
				FnSessionCreate,
				FnSessionRevoke,
				FnAPITokenIssue,
				FnAPITokenValidate,
				FnAdminAPITokenImport,
				FnAdminAPITokenRevoke,
				FnAdminSourceImport,
				FnAdminSourceRotate,
				FnAdminSourceRevoke,
				FnSourceValidate,
			},
			Queries: []string{
				FnSessionValidate,
				FnAdminAPITokenList,
				FnProjection,
				FnAdminAuditQuery,
			},
			SecretRule: "caller-generated plaintext credentials enter private command providers once; only SHA-256 digests are persisted",
		})
	})
	pulp.Provide(FnAdminIdentityImport, provide(func(v AdminIdentityImportRequest) (IdentityImportResult, error) {
		return owner.ImportIdentity(context.Background(), v)
	}))
	pulp.Provide(FnMagicLinkIssue, provide(func(v MagicLinkIssueRequest) (MagicLinkIssueResult, error) {
		return owner.IssueMagicLink(context.Background(), v)
	}))
	pulp.Provide(FnMagicLinkConsume, provide(func(v MagicLinkConsumeRequest) (MagicLinkConsumeResult, error) {
		return owner.ConsumeMagicLink(context.Background(), v)
	}))
	pulp.Provide(FnSessionCreate, provide(func(v SessionCreateRequest) (SessionCreateResult, error) {
		return owner.CreateSession(context.Background(), v)
	}))
	pulp.Provide(FnSessionValidate, provide(func(v SessionValidateRequest) (SessionValidateResult, error) {
		return owner.ValidateSession(context.Background(), v)
	}))
	pulp.Provide(FnSessionRevoke, provide(func(v SessionRevokeRequest) (SessionRevokeResult, error) {
		return owner.RevokeSession(context.Background(), v)
	}))
	pulp.Provide(FnAPITokenIssue, provide(func(v APITokenIssueRequest) (APITokenMutationResult, error) {
		return owner.IssueAPIToken(context.Background(), v)
	}))
	pulp.Provide(FnAPITokenValidate, provide(func(v APITokenValidateRequest) (APITokenValidateResult, error) {
		return owner.ValidateAPIToken(context.Background(), v)
	}))
	pulp.Provide(FnAdminAPITokenImport, provide(func(v APITokenImportRequest) (APITokenMutationResult, error) {
		return owner.ImportAPIToken(context.Background(), v)
	}))
	pulp.Provide(FnAdminAPITokenList, provide(func(v APITokenListRequest) (APITokenListResult, error) {
		return owner.ListAPITokens(context.Background(), v)
	}))
	pulp.Provide(FnAdminAPITokenRevoke, provide(func(v APITokenRevokeRequest) (APITokenMutationResult, error) {
		return owner.RevokeAPIToken(context.Background(), v)
	}))
	pulp.Provide(FnAdminSourceImport, provide(func(v SourceCredentialImportRequest) (SourceCredentialMutationResult, error) {
		return owner.ImportSourceCredential(context.Background(), v)
	}))
	pulp.Provide(FnAdminSourceRotate, provide(func(v SourceCredentialRotateRequest) (SourceCredentialMutationResult, error) {
		return owner.RotateSourceCredential(context.Background(), v)
	}))
	pulp.Provide(FnAdminSourceRevoke, provide(func(v SourceCredentialRevokeRequest) (SourceCredentialMutationResult, error) {
		return owner.RevokeSourceCredential(context.Background(), v)
	}))
	pulp.Provide(FnSourceValidate, provide(func(v SourceCredentialValidateRequest) (SourceCredentialValidateResult, error) {
		return owner.ValidateSourceCredential(context.Background(), v)
	}))
	pulp.Provide(FnProjection, provide(func(v ProjectionRequest) (Projection, error) {
		return owner.Projection(context.Background(), v)
	}))
	pulp.Provide(FnAdminAuditQuery, provide(func(v AdminAuditQueryRequest) (AdminAuditQueryResult, error) {
		return owner.QueryAudit(context.Background(), v)
	}))
	return nil
}

func provide[I any, O any](handler func(I) (O, error)) pulp.Provider {
	return func(raw []byte) ([]byte, error) {
		var input I
		if err := msgpack.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		output, err := handler(input)
		if err != nil {
			return nil, err
		}
		return msgpack.Marshal(output)
	}
}
