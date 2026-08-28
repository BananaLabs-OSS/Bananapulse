package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const sourceLifecycleContractVersion = "bananapulse.host.source/v1"

const (
	eventHostSourceAdminCreate = "bananapulse.host.source.admin.create.v1"
	eventHostSourceAdminRotate = "bananapulse.host.source.admin.rotate.v1"
	eventHostSourceAdminRevoke = "bananapulse.host.source.admin.revoke.v1"
)

type sourceAdminCreateRequest struct {
	Version      string              `json:"version"`
	RequestID    string              `json:"request_id"`
	Source       bridgeMonitorSource `json:"source"`
	CredentialID string              `json:"credential_id"`
	Token        string              `json:"token"`
	ActorID      string              `json:"actor_id,omitempty"`
	CreatedAt    time.Time           `json:"created_at"`
	ExpiresAt    *time.Time          `json:"expires_at,omitempty"`
}

type sourceAdminRevokeRequest struct {
	Version      string    `json:"version"`
	RequestID    string    `json:"request_id"`
	SourceID     string    `json:"source_id"`
	CredentialID string    `json:"credential_id,omitempty"`
	ActorID      string    `json:"actor_id,omitempty"`
	RevokedAt    time.Time `json:"revoked_at"`
}

type sourceLifecycleResult struct {
	Version      string `json:"version"`
	RequestID    string `json:"request_id"`
	SourceID     string `json:"source_id"`
	CredentialID string `json:"credential_id,omitempty"`
	Completed    bool   `json:"completed"`
	Deduped      bool   `json:"deduped"`
}

type sourceLifecycleClient interface {
	callRaw(context.Context, string, any, any) error
	callProviderRaw(context.Context, string, string, any, any) error
}

type sourceSagaCheckpoint struct {
	RequestID     string
	Kind          string
	SourceID      string
	CredentialID  string
	Fingerprint   string
	AuthDone      bool
	MonitorDone   bool
	Compensated   bool
	FailureCode   string
	FailureStatus int
}

type sourceSagaCheckpointStore interface {
	Load(context.Context, string) (sourceSagaCheckpoint, error)
	Save(context.Context, sourceSagaCheckpoint) error
}

type sourceLifecycleService struct {
	client sourceLifecycleClient
	store  sourceSagaCheckpointStore
}

func newSourceLifecycleService(client sourceLifecycleClient, store sourceSagaCheckpointStore) (*sourceLifecycleService, error) {
	if client == nil || store == nil {
		return nil, errors.New("source lifecycle client and checkpoint store are required")
	}
	return &sourceLifecycleService{client: client, store: store}, nil
}

func (s *sourceLifecycleService) Create(ctx context.Context, request sourceAdminCreateRequest) (sourceLifecycleResult, error) {
	if request.Version != sourceLifecycleContractVersion || request.RequestID == "" ||
		request.Source.ID == "" || request.CredentialID == "" || request.Token == "" || request.CreatedAt.IsZero() {
		return sourceLifecycleResult{}, &bridgeRequestError{message: "invalid source create request"}
	}
	fingerprint, err := sourceLifecycleFingerprint(request)
	if err != nil {
		return sourceLifecycleResult{}, err
	}
	checkpoint, deduped, err := s.loadBound(
		ctx, request.RequestID, "create", request.Source.ID, request.CredentialID, fingerprint,
	)
	if err != nil {
		return sourceLifecycleResult{}, err
	}
	if checkpoint.Compensated {
		return sourceLifecycleResult{}, persistedSourceFailure(checkpoint)
	}
	if !checkpoint.AuthDone {
		var result any
		err := s.client.callProviderRaw(ctx, authOwnerCell, providerAuthSourceAdminImport, bridgeAuthSourceCredentialImportRequest{
			Version: "bananapulse.auth/v1", RequestID: request.RequestID + "/credential",
			CredentialID: request.CredentialID, SourceID: request.Source.ID, Token: request.Token,
			ActorID: request.ActorID, CreatedAt: request.CreatedAt, ExpiresAt: request.ExpiresAt,
		}, &result)
		if err != nil {
			return sourceLifecycleResult{}, &bridgeDispatchError{event: eventHostAuthSourceAdminImport, err: err}
		}
		checkpoint.AuthDone = true
		if err := s.store.Save(ctx, checkpoint); err != nil {
			return sourceLifecycleResult{}, fmt.Errorf("save source create credential checkpoint: %w", err)
		}
	}
	if !checkpoint.MonitorDone {
		var result any
		command := bridgeMonitorCommand{
			Version: "monitor.v1", ID: request.RequestID + "/monitor", Kind: "upsert_source",
			AtUnix: request.CreatedAt.Unix(), Source: &request.Source,
		}
		if err := s.client.callRaw(ctx, eventMonitorAdminCommand, command, &result); err != nil {
			dispatchErr := &bridgeDispatchError{event: eventMonitorAdminCommand, err: err}
			if domain, ok := classifyBridgeDomainError(dispatchErr); ok {
				if compensateErr := s.compensateCreate(ctx, request); compensateErr != nil {
					return sourceLifecycleResult{}, compensateErr
				}
				checkpoint.Compensated = true
				checkpoint.FailureCode = domain.Code
				checkpoint.FailureStatus = domain.Status
				if err := s.store.Save(ctx, checkpoint); err != nil {
					return sourceLifecycleResult{}, fmt.Errorf("save source create compensation checkpoint: %w", err)
				}
				return sourceLifecycleResult{}, &domain
			}
			return sourceLifecycleResult{}, dispatchErr
		}
		checkpoint.MonitorDone = true
		if err := s.store.Save(ctx, checkpoint); err != nil {
			return sourceLifecycleResult{}, fmt.Errorf("save source create monitor checkpoint: %w", err)
		}
	}
	return sourceLifecycleResult{
		Version: sourceLifecycleContractVersion, RequestID: request.RequestID,
		SourceID: request.Source.ID, CredentialID: request.CredentialID, Completed: true, Deduped: deduped,
	}, nil
}

func (s *sourceLifecycleService) Rotate(ctx context.Context, request bridgeAuthSourceCredentialRotateRequest) (any, error) {
	if request.Version != "bananapulse.auth/v1" || request.RequestID == "" ||
		request.CredentialID == "" || request.SourceID == "" || request.Token == "" {
		return nil, &bridgeRequestError{message: "invalid source rotate request"}
	}
	var result any
	if err := s.client.callProviderRaw(
		ctx, authOwnerCell, providerAuthSourceAdminRotate, request, &result,
	); err != nil {
		return nil, &bridgeDispatchError{event: eventHostAuthSourceAdminRotate, err: err}
	}
	return result, nil
}

func (s *sourceLifecycleService) Revoke(ctx context.Context, request sourceAdminRevokeRequest) (sourceLifecycleResult, error) {
	if request.Version != sourceLifecycleContractVersion || request.RequestID == "" ||
		request.SourceID == "" || request.RevokedAt.IsZero() {
		return sourceLifecycleResult{}, &bridgeRequestError{message: "invalid source revoke request"}
	}
	fingerprint, err := sourceLifecycleFingerprint(request)
	if err != nil {
		return sourceLifecycleResult{}, err
	}
	checkpoint, deduped, err := s.loadBound(
		ctx, request.RequestID, "revoke", request.SourceID, request.CredentialID, fingerprint,
	)
	if err != nil {
		return sourceLifecycleResult{}, err
	}
	if !checkpoint.MonitorDone {
		var result any
		err := s.client.callRaw(ctx, eventMonitorAdminCommand, bridgeMonitorCommand{
			Version: "monitor.v1", ID: request.RequestID + "/monitor", Kind: "revoke_source",
			AtUnix: request.RevokedAt.Unix(), SourceID: request.SourceID,
		}, &result)
		if err != nil {
			return sourceLifecycleResult{}, &bridgeDispatchError{event: eventMonitorAdminCommand, err: err}
		}
		checkpoint.MonitorDone = true
		if err := s.store.Save(ctx, checkpoint); err != nil {
			return sourceLifecycleResult{}, fmt.Errorf("save source revoke monitor checkpoint: %w", err)
		}
	}
	if !checkpoint.AuthDone {
		var result any
		err := s.client.callProviderRaw(ctx, authOwnerCell, providerAuthSourceAdminRevoke, bridgeAuthSourceCredentialRevokeRequest{
			Version: "bananapulse.auth/v1", RequestID: request.RequestID + "/credential",
			CredentialID: request.CredentialID, SourceID: request.SourceID,
			ActorID: request.ActorID, RevokedAt: request.RevokedAt,
		}, &result)
		if err != nil {
			return sourceLifecycleResult{}, &bridgeDispatchError{event: eventHostAuthSourceAdminRevoke, err: err}
		}
		checkpoint.AuthDone = true
		if err := s.store.Save(ctx, checkpoint); err != nil {
			return sourceLifecycleResult{}, fmt.Errorf("save source revoke credential checkpoint: %w", err)
		}
	}
	return sourceLifecycleResult{
		Version: sourceLifecycleContractVersion, RequestID: request.RequestID,
		SourceID: request.SourceID, CredentialID: request.CredentialID, Completed: true, Deduped: deduped,
	}, nil
}

func (s *sourceLifecycleService) compensateCreate(ctx context.Context, request sourceAdminCreateRequest) error {
	var result any
	err := s.client.callProviderRaw(ctx, authOwnerCell, providerAuthSourceAdminRevoke, bridgeAuthSourceCredentialRevokeRequest{
		Version: "bananapulse.auth/v1", RequestID: request.RequestID + "/compensate",
		CredentialID: request.CredentialID, SourceID: request.Source.ID,
		ActorID: request.ActorID, RevokedAt: request.CreatedAt,
	}, &result)
	if err != nil {
		return &bridgeDispatchError{event: eventHostAuthSourceAdminRevoke, err: err}
	}
	return nil
}

func (s *sourceLifecycleService) loadBound(
	ctx context.Context,
	requestID string,
	kind string,
	sourceID string,
	credentialID string,
	fingerprint string,
) (sourceSagaCheckpoint, bool, error) {
	checkpoint, err := s.store.Load(ctx, requestID)
	if err != nil {
		return sourceSagaCheckpoint{}, false, fmt.Errorf("load source lifecycle checkpoint: %w", err)
	}
	if checkpoint.RequestID == "" {
		checkpoint = sourceSagaCheckpoint{
			RequestID: requestID, Kind: kind, SourceID: sourceID, CredentialID: credentialID, Fingerprint: fingerprint,
		}
		if err := s.store.Save(ctx, checkpoint); err != nil {
			return sourceSagaCheckpoint{}, false, fmt.Errorf("save source lifecycle checkpoint: %w", err)
		}
		return checkpoint, false, nil
	}
	if checkpoint.Kind != kind || checkpoint.SourceID != sourceID ||
		checkpoint.CredentialID != credentialID || checkpoint.Fingerprint != fingerprint {
		return sourceSagaCheckpoint{}, false, &bridgeRequestError{message: "request_id is bound to another source lifecycle command"}
	}
	return checkpoint, true, nil
}

func sourceLifecycleFingerprint(request any) (string, error) {
	wire, err := json.Marshal(request)
	if err != nil {
		return "", errors.New("source lifecycle request cannot be fingerprinted")
	}
	sum := sha256.Sum256(wire)
	return hex.EncodeToString(sum[:]), nil
}

func persistedSourceFailure(checkpoint sourceSagaCheckpoint) error {
	status := checkpoint.FailureStatus
	if status == 0 {
		status = 409
	}
	code := checkpoint.FailureCode
	if code == "" {
		code = "conflict"
	}
	return &bridgeDomainError{Status: status, Code: code, Message: "source creation was compensated"}
}

type sqliteSourceSagaCheckpointStore struct {
	db *sql.DB
}

func newSQLiteSourceSagaCheckpointStore(db *sql.DB) (*sqliteSourceSagaCheckpointStore, error) {
	if db == nil {
		return nil, errors.New("source saga checkpoint database is required")
	}
	store := &sqliteSourceSagaCheckpointStore{db: db}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS pulp_source_sagas (
			request_id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			source_id TEXT NOT NULL,
			credential_id TEXT NOT NULL,
			fingerprint TEXT NOT NULL,
			auth_done INTEGER NOT NULL,
			monitor_done INTEGER NOT NULL,
			compensated INTEGER NOT NULL,
			failure_code TEXT NOT NULL,
			failure_status INTEGER NOT NULL
		)`); err != nil {
		return nil, fmt.Errorf("migrate source saga checkpoints: %w", err)
	}
	return store, nil
}

func (s *sqliteSourceSagaCheckpointStore) Load(ctx context.Context, requestID string) (sourceSagaCheckpoint, error) {
	var checkpoint sourceSagaCheckpoint
	var authDone, monitorDone, compensated int
	err := s.db.QueryRowContext(ctx, `
		SELECT request_id,kind,source_id,credential_id,fingerprint,auth_done,monitor_done,compensated,failure_code,failure_status
		FROM pulp_source_sagas WHERE request_id = ?`, requestID,
	).Scan(
		&checkpoint.RequestID, &checkpoint.Kind, &checkpoint.SourceID, &checkpoint.CredentialID, &checkpoint.Fingerprint,
		&authDone, &monitorDone, &compensated, &checkpoint.FailureCode, &checkpoint.FailureStatus,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return sourceSagaCheckpoint{}, nil
	}
	if err != nil {
		return sourceSagaCheckpoint{}, err
	}
	checkpoint.AuthDone = authDone != 0
	checkpoint.MonitorDone = monitorDone != 0
	checkpoint.Compensated = compensated != 0
	return checkpoint, nil
}

func (s *sqliteSourceSagaCheckpointStore) Save(ctx context.Context, checkpoint sourceSagaCheckpoint) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO pulp_source_sagas(
			request_id,kind,source_id,credential_id,fingerprint,auth_done,monitor_done,compensated,failure_code,failure_status
		) VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(request_id) DO UPDATE SET
			auth_done=excluded.auth_done,
			monitor_done=excluded.monitor_done,
			compensated=excluded.compensated,
			failure_code=excluded.failure_code,
			failure_status=excluded.failure_status`,
		checkpoint.RequestID, checkpoint.Kind, checkpoint.SourceID, checkpoint.CredentialID,
		checkpoint.Fingerprint,
		boolInt(checkpoint.AuthDone), boolInt(checkpoint.MonitorDone), boolInt(checkpoint.Compensated),
		checkpoint.FailureCode, checkpoint.FailureStatus,
	)
	return err
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
