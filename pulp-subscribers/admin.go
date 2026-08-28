package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

func adminSubscriber(id, email, state string, createdAt int64, confirmedAt sql.NullInt64) AdminSubscriber {
	result := AdminSubscriber{
		ID:        id,
		Email:     email,
		State:     state,
		CreatedAt: time.Unix(0, createdAt).UTC(),
	}
	if confirmedAt.Valid {
		value := time.Unix(0, confirmedAt.Int64).UTC()
		result.ConfirmedAt = &value
	}
	return result
}

func (o *Owner) AdminList(ctx context.Context, request AdminListRequest) (AdminSubscriberList, error) {
	if err := validVersion(request.Version); err != nil {
		return AdminSubscriberList{}, err
	}
	rows, err := o.store.db.QueryContext(ctx, `SELECT id,email,state,created_at,confirmed_at FROM subscribers ORDER BY created_at,id`)
	if err != nil {
		return AdminSubscriberList{}, err
	}
	defer rows.Close()
	result := AdminSubscriberList{Version: ContractVersion, Subscribers: []AdminSubscriber{}}
	for rows.Next() {
		var id, email, state string
		var createdAt int64
		var confirmedAt sql.NullInt64
		if err := rows.Scan(&id, &email, &state, &createdAt, &confirmedAt); err != nil {
			return AdminSubscriberList{}, err
		}
		result.Subscribers = append(result.Subscribers, adminSubscriber(id, email, state, createdAt, confirmedAt))
	}
	return result, rows.Err()
}

func (o *Owner) AdminGet(ctx context.Context, request AdminGetRequest) (AdminSubscriberGet, error) {
	if err := validVersion(request.Version); err != nil {
		return AdminSubscriberGet{}, err
	}
	if err := require("subscriber id", request.SubscriberID); err != nil {
		return AdminSubscriberGet{}, err
	}
	var id, email, state string
	var createdAt int64
	var confirmedAt sql.NullInt64
	err := o.store.db.QueryRowContext(ctx, `SELECT id,email,state,created_at,confirmed_at FROM subscribers WHERE id=?`, request.SubscriberID).
		Scan(&id, &email, &state, &createdAt, &confirmedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminSubscriberGet{Version: ContractVersion}, nil
	}
	if err != nil {
		return AdminSubscriberGet{}, err
	}
	subscriber := adminSubscriber(id, email, state, createdAt, confirmedAt)
	return AdminSubscriberGet{Version: ContractVersion, Found: true, Subscriber: &subscriber}, nil
}

func cachedAdminMutation(ctx context.Context, tx *sql.Tx, requestID, kind, payloadHash string) (AdminMutationResult, bool, error) {
	var priorKind, priorHash string
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT command_kind,payload_hash,response FROM subscriber_admin_receipts WHERE request_id=?`, requestID).
		Scan(&priorKind, &priorHash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminMutationResult{}, false, nil
	}
	if err != nil {
		return AdminMutationResult{}, false, err
	}
	if priorKind != kind || priorHash != payloadHash {
		return AdminMutationResult{}, false, errors.New("admin request id was reused with a different command")
	}
	var result AdminMutationResult
	if err := msgpack.Unmarshal(raw, &result); err != nil {
		return AdminMutationResult{}, false, fmt.Errorf("decode admin command receipt: %w", err)
	}
	return result, true, nil
}

func saveAdminMutation(ctx context.Context, tx *sql.Tx, requestID, kind, payloadHash string, result AdminMutationResult) error {
	raw, err := msgpack.Marshal(result)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO subscriber_admin_receipts(request_id,command_kind,payload_hash,response) VALUES (?,?,?,?)`, requestID, kind, payloadHash, raw)
	return err
}

func commandPayloadHash(value any) (string, error) {
	raw, err := msgpack.Marshal(value)
	if err != nil {
		return "", err
	}
	return secretHash(string(raw)), nil
}

func (o *Owner) AdminDelete(ctx context.Context, request AdminDeleteRequest) (AdminMutationResult, error) {
	if err := validVersion(request.Version); err != nil {
		return AdminMutationResult{}, err
	}
	if err := require("request id", request.RequestID); err != nil {
		return AdminMutationResult{}, err
	}
	if err := require("subscriber id", request.SubscriberID); err != nil {
		return AdminMutationResult{}, err
	}
	payloadHash, err := commandPayloadHash(request)
	if err != nil {
		return AdminMutationResult{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminMutationResult{}, err
	}
	defer tx.Rollback()
	if cached, ok, err := cachedAdminMutation(ctx, tx, request.RequestID, "delete", payloadHash); err != nil || ok {
		return cached, err
	}
	var found int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM subscribers WHERE id=?`, request.SubscriberID).Scan(&found); err != nil {
		return AdminMutationResult{}, err
	}
	if found != 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM subscriber_outbox WHERE subscriber_id=?`, request.SubscriberID); err != nil {
			return AdminMutationResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM subscriber_private_tokens WHERE subscriber_id=?`, request.SubscriberID); err != nil {
			return AdminMutationResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM subscribers WHERE id=?`, request.SubscriberID); err != nil {
			return AdminMutationResult{}, err
		}
	}
	result := AdminMutationResult{Version: ContractVersion, Found: found != 0, Changed: found != 0}
	if err := saveAdminMutation(ctx, tx, request.RequestID, "delete", payloadHash, result); err != nil {
		return AdminMutationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminMutationResult{}, err
	}
	return result, nil
}

func (o *Owner) AdminStateSet(ctx context.Context, request AdminStateSetRequest) (AdminMutationResult, error) {
	if err := validVersion(request.Version); err != nil {
		return AdminMutationResult{}, err
	}
	if err := require("request id", request.RequestID); err != nil {
		return AdminMutationResult{}, err
	}
	if err := require("subscriber id", request.SubscriberID); err != nil {
		return AdminMutationResult{}, err
	}
	state := strings.ToLower(strings.TrimSpace(request.State))
	if state != "pending" && state != "confirmed" && state != "unsubscribed" {
		return AdminMutationResult{}, errors.New("subscriber state must be pending, confirmed, or unsubscribed")
	}
	request.State = state
	payloadHash, err := commandPayloadHash(request)
	if err != nil {
		return AdminMutationResult{}, err
	}
	changedAt := request.ChangedAt.UTC()
	if changedAt.IsZero() {
		changedAt = time.Now().UTC()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminMutationResult{}, err
	}
	defer tx.Rollback()
	if cached, ok, err := cachedAdminMutation(ctx, tx, request.RequestID, "state.set", payloadHash); err != nil || ok {
		return cached, err
	}
	var current string
	err = tx.QueryRowContext(ctx, `SELECT state FROM subscribers WHERE id=?`, request.SubscriberID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		result := AdminMutationResult{Version: ContractVersion}
		if err := saveAdminMutation(ctx, tx, request.RequestID, "state.set", payloadHash, result); err != nil {
			return AdminMutationResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return AdminMutationResult{}, err
		}
		return result, nil
	}
	if err != nil {
		return AdminMutationResult{}, err
	}
	var confirmedAt any
	if state == "confirmed" {
		confirmedAt = changedAt.UnixNano()
	}
	if current != state {
		if _, err := tx.ExecContext(ctx, `UPDATE subscribers SET state=?,confirmed_at=? WHERE id=?`, state, confirmedAt, request.SubscriberID); err != nil {
			return AdminMutationResult{}, err
		}
	}
	result := AdminMutationResult{Version: ContractVersion, Found: true, Changed: current != state, State: state}
	if err := saveAdminMutation(ctx, tx, request.RequestID, "state.set", payloadHash, result); err != nil {
		return AdminMutationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminMutationResult{}, err
	}
	return result, nil
}

func (o *Owner) ImportLegacy(ctx context.Context, request MigrationImportRequest) (MigrationImportReceipt, error) {
	if err := validVersion(request.Version); err != nil {
		return MigrationImportReceipt{}, err
	}
	if err := require("request id", request.RequestID); err != nil {
		return MigrationImportReceipt{}, err
	}
	payload, err := msgpack.Marshal(request.Subscribers)
	if err != nil {
		return MigrationImportReceipt{}, err
	}
	payloadDigest := secretHash(string(payload))
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return MigrationImportReceipt{}, err
	}
	defer tx.Rollback()
	var priorDigest string
	var priorRaw []byte
	err = tx.QueryRowContext(ctx, `SELECT payload_hash,response FROM subscriber_migration_receipts WHERE request_id=?`, request.RequestID).Scan(&priorDigest, &priorRaw)
	if err == nil {
		if priorDigest != payloadDigest {
			return MigrationImportReceipt{}, errors.New("migration request id was reused with a different payload")
		}
		var receipt MigrationImportReceipt
		if err := msgpack.Unmarshal(priorRaw, &receipt); err != nil {
			return MigrationImportReceipt{}, err
		}
		return receipt, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return MigrationImportReceipt{}, err
	}
	receipt := MigrationImportReceipt{Version: ContractVersion, RequestID: request.RequestID}
	for _, row := range request.Subscribers {
		if err := require("legacy subscriber id", row.ID); err != nil {
			return MigrationImportReceipt{}, err
		}
		email, err := normalizedEmail(row.Email)
		if err != nil {
			return MigrationImportReceipt{}, fmt.Errorf("legacy subscriber %q: %w", row.ID, err)
		}
		createdAt := row.CreatedAt.UTC()
		if createdAt.IsZero() {
			return MigrationImportReceipt{}, fmt.Errorf("legacy subscriber %q: created at is required", row.ID)
		}
		state := "pending"
		var confirmedAt any
		if row.ConfirmedAt != nil {
			value := row.ConfirmedAt.UTC()
			state, confirmedAt = "confirmed", value.UnixNano()
		}
		var existingID, existingEmail, existingState string
		var existingCreated int64
		var existingConfirmed sql.NullInt64
		err = tx.QueryRowContext(ctx, `SELECT id,email,state,created_at,confirmed_at FROM subscribers WHERE id=? OR email=?`, row.ID, email).
			Scan(&existingID, &existingEmail, &existingState, &existingCreated, &existingConfirmed)
		if err == nil {
			wantConfirmed, haveConfirmed := confirmedAt != nil, existingConfirmed.Valid
			same := existingID == row.ID && existingEmail == email && existingState == state &&
				existingCreated == createdAt.UnixNano() && wantConfirmed == haveConfirmed
			if same && wantConfirmed {
				same = existingConfirmed.Int64 == confirmedAt.(int64)
			}
			if !same {
				return MigrationImportReceipt{}, fmt.Errorf("legacy subscriber %q conflicts with owner state", row.ID)
			}
			receipt.Unchanged++
		} else if errors.Is(err, sql.ErrNoRows) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO subscribers(id,email,confirmation_hash,unsubscribe_hash,state,created_at,confirmed_at) VALUES (?,?,?,?,?,?,?)`,
				row.ID, email, secretHash(row.ID), secretHash(row.ID), state, createdAt.UnixNano(), confirmedAt); err != nil {
				return MigrationImportReceipt{}, err
			}
			receipt.Imported++
		} else {
			return MigrationImportReceipt{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO subscriber_private_tokens(subscriber_id,unsubscribe_token) VALUES (?,?)`, row.ID, row.ID); err != nil {
			return MigrationImportReceipt{}, err
		}
	}
	raw, err := msgpack.Marshal(receipt)
	if err != nil {
		return MigrationImportReceipt{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO subscriber_migration_receipts(request_id,payload_hash,response,recorded_at) VALUES (?,?,?,?)`,
		request.RequestID, payloadDigest, raw, time.Now().UTC().UnixNano()); err != nil {
		return MigrationImportReceipt{}, err
	}
	if err := tx.Commit(); err != nil {
		return MigrationImportReceipt{}, err
	}
	return receipt, nil
}
