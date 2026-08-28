package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

type Owner struct {
	store *Store
	mu    sync.Mutex // serializes the command check/write path for a single cell.
}

func OpenOwner(store *Store) (*Owner, error) {
	if store == nil || store.db == nil {
		return nil, errors.New("subscriber store is required")
	}
	if err := migrate(context.Background(), store.db); err != nil {
		return nil, fmt.Errorf("migrate subscriber state: %w", err)
	}
	return &Owner{store: store}, nil
}

func validVersion(v string) error {
	if v != ContractVersion {
		return fmt.Errorf("unsupported subscriber contract %q", v)
	}
	return nil
}
func require(label, v string) error {
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("%s is required", label)
	}
	return nil
}
func secretHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func opaqueID(prefix, value string) string {
	sum := sha256.Sum256([]byte(value))
	return prefix + hex.EncodeToString(sum[:12])
}
func normalizedEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if len(email) < 3 || len(email) > 320 || strings.Count(email, "@") != 1 || strings.ContainsAny(email, "\r\n") {
		return "", errors.New("invalid email")
	}
	parts := strings.Split(email, "@")
	if parts[0] == "" || parts[1] == "" || !strings.Contains(parts[1], ".") {
		return "", errors.New("invalid email")
	}
	return email, nil
}

func cachedResult(ctx context.Context, tx *sql.Tx, requestID string) (CommandResult, bool, error) {
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT response FROM subscriber_commands WHERE request_id = ?`, requestID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return CommandResult{}, false, nil
	}
	if err != nil {
		return CommandResult{}, false, err
	}
	var result CommandResult
	if err := msgpack.Unmarshal(raw, &result); err != nil {
		return CommandResult{}, false, fmt.Errorf("decode command receipt: %w", err)
	}
	return result, true, nil
}
func saveResult(ctx context.Context, tx *sql.Tx, requestID string, result CommandResult) error {
	raw, err := msgpack.Marshal(result)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO subscriber_commands(request_id, response) VALUES (?, ?)`, requestID, raw)
	return err
}

func (o *Owner) Subscribe(ctx context.Context, request SubscribeRequest) (CommandResult, error) {
	if err := validVersion(request.Version); err != nil {
		return CommandResult{}, err
	}
	if err := require("request id", request.RequestID); err != nil {
		return CommandResult{}, err
	}
	if err := require("confirmation token", request.ConfirmationToken); err != nil {
		return CommandResult{}, err
	}
	if err := require("unsubscribe token", request.UnsubscribeToken); err != nil {
		return CommandResult{}, err
	}
	email, err := normalizedEmail(request.Email)
	if err != nil {
		return CommandResult{}, err
	}
	now := request.RequestedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return CommandResult{}, err
	}
	defer tx.Rollback()
	if cached, ok, err := cachedResult(ctx, tx, request.RequestID); err != nil || ok {
		if err != nil {
			return CommandResult{}, err
		}
		return cached, nil
	}
	confirmationHash, unsubscribeHash := secretHash(request.ConfirmationToken), secretHash(request.UnsubscribeToken)
	var existingID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM subscribers WHERE email = ?`, email).Scan(&existingID)
	if err == nil {
		result := CommandResult{Version: ContractVersion, SubscriberID: existingID}
		if err := saveResult(ctx, tx, request.RequestID, result); err != nil {
			return CommandResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return CommandResult{}, err
		}
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CommandResult{}, err
	}
	id := opaqueID("sub_", request.RequestID)
	if _, err := tx.ExecContext(ctx, `INSERT INTO subscribers(id,email,confirmation_hash,unsubscribe_hash,state,created_at) VALUES (?,?,?,?,?,?)`, id, email, confirmationHash, unsubscribeHash, "pending", now.UnixNano()); err != nil {
		return CommandResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO subscriber_private_tokens(subscriber_id,unsubscribe_token) VALUES (?,?)`, id, request.UnsubscribeToken); err != nil {
		return CommandResult{}, err
	}
	subject := strings.TrimSpace(request.ConfirmationSubject)
	if subject == "" {
		subject = "Confirm status subscription"
	}
	body := strings.TrimSpace(request.ConfirmationBody)
	if body == "" {
		body = "Confirm your subscription using the supplied confirmation token."
	}
	if err := insertIntent(ctx, tx, id, "confirmation:"+confirmationHash, "subscriber.confirmation", email, subject, body, now); err != nil {
		return CommandResult{}, err
	}
	result := CommandResult{Version: ContractVersion, SubscriberID: id, Created: true, IntentCount: 1}
	if err := saveResult(ctx, tx, request.RequestID, result); err != nil {
		return CommandResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return CommandResult{}, err
	}
	return result, nil
}

func cachedResendResult(ctx context.Context, tx *sql.Tx, requestID, payloadHash string) (ConfirmationResendResult, bool, error) {
	var priorHash string
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT payload_hash,response FROM subscriber_resend_receipts WHERE request_id=?`, requestID).Scan(&priorHash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ConfirmationResendResult{}, false, nil
	}
	if err != nil {
		return ConfirmationResendResult{}, false, err
	}
	if priorHash != payloadHash {
		return ConfirmationResendResult{}, false, errors.New("resend request id was reused with a different payload")
	}
	var result ConfirmationResendResult
	if err := msgpack.Unmarshal(raw, &result); err != nil {
		return ConfirmationResendResult{}, false, fmt.Errorf("decode resend receipt: %w", err)
	}
	return result, true, nil
}

func (o *Owner) ResendConfirmation(ctx context.Context, request ConfirmationResendRequest) (ConfirmationResendResult, error) {
	if err := validVersion(request.Version); err != nil {
		return ConfirmationResendResult{}, err
	}
	if err := require("request id", request.RequestID); err != nil {
		return ConfirmationResendResult{}, err
	}
	if err := require("confirmation token", request.ConfirmationToken); err != nil {
		return ConfirmationResendResult{}, err
	}
	email, err := normalizedEmail(request.Email)
	if err != nil {
		return ConfirmationResendResult{}, err
	}
	now := request.RequestedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return ConfirmationResendResult{}, err
	}
	defer tx.Rollback()
	payload, err := msgpack.Marshal(request)
	if err != nil {
		return ConfirmationResendResult{}, err
	}
	payloadHash := secretHash(string(payload))
	if cached, ok, err := cachedResendResult(ctx, tx, request.RequestID, payloadHash); err != nil || ok {
		return cached, err
	}
	var subscriberID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM subscribers WHERE email=? AND confirmation_hash=? AND state='pending'`,
		email, secretHash(request.ConfirmationToken)).Scan(&subscriberID)
	if err == nil {
		subject := strings.TrimSpace(request.ConfirmationSubject)
		if subject == "" {
			subject = "Confirm status subscription"
		}
		body := strings.TrimSpace(request.ConfirmationBody)
		if body == "" {
			body = "Confirm your subscription using the supplied confirmation token."
		}
		if err := insertIntent(ctx, tx, subscriberID, "confirmation-resend:"+request.RequestID, "subscriber.confirmation", email, subject, body, now); err != nil {
			return ConfirmationResendResult{}, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return ConfirmationResendResult{}, err
	}
	// The result is deliberately identical for unknown, confirmed, and pending
	// addresses. Delivery occurs only through the private outbox.
	result := ConfirmationResendResult{Version: ContractVersion, Accepted: true}
	raw, err := msgpack.Marshal(result)
	if err != nil {
		return ConfirmationResendResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO subscriber_resend_receipts(request_id,payload_hash,response) VALUES (?,?,?)`, request.RequestID, payloadHash, raw); err != nil {
		return ConfirmationResendResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ConfirmationResendResult{}, err
	}
	return result, nil
}

func (o *Owner) Confirm(ctx context.Context, request ConfirmRequest) (CommandResult, error) {
	if err := validVersion(request.Version); err != nil {
		return CommandResult{}, err
	}
	if err := require("request id", request.RequestID); err != nil {
		return CommandResult{}, err
	}
	if err := require("token", request.Token); err != nil {
		return CommandResult{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return CommandResult{}, err
	}
	defer tx.Rollback()
	if cached, ok, err := cachedResult(ctx, tx, request.RequestID); err != nil || ok {
		if err != nil {
			return CommandResult{}, err
		}
		return cached, nil
	}
	var id, state string
	err = tx.QueryRowContext(ctx, `SELECT id,state FROM subscribers WHERE confirmation_hash = ?`, secretHash(request.Token)).Scan(&id, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return CommandResult{}, errors.New("confirmation token is invalid")
	}
	if err != nil {
		return CommandResult{}, err
	}
	if state == "pending" {
		if _, err := tx.ExecContext(ctx, `UPDATE subscribers SET state='confirmed', confirmed_at=? WHERE id=?`, time.Now().UTC().UnixNano(), id); err != nil {
			return CommandResult{}, err
		}
	}
	result := CommandResult{Version: ContractVersion, SubscriberID: id, Confirmed: state != "unsubscribed"}
	if err := saveResult(ctx, tx, request.RequestID, result); err != nil {
		return CommandResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return CommandResult{}, err
	}
	return result, nil
}

func (o *Owner) Unsubscribe(ctx context.Context, request UnsubscribeRequest) (CommandResult, error) {
	if err := validVersion(request.Version); err != nil {
		return CommandResult{}, err
	}
	if err := require("request id", request.RequestID); err != nil {
		return CommandResult{}, err
	}
	if err := require("token", request.Token); err != nil {
		return CommandResult{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return CommandResult{}, err
	}
	defer tx.Rollback()
	if cached, ok, err := cachedResult(ctx, tx, request.RequestID); err != nil || ok {
		if err != nil {
			return CommandResult{}, err
		}
		return cached, nil
	}
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM subscribers WHERE unsubscribe_hash = ?`, secretHash(request.Token)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return CommandResult{}, errors.New("unsubscribe token is invalid")
	}
	if err != nil {
		return CommandResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE subscribers SET state='unsubscribed' WHERE id=?`, id); err != nil {
		return CommandResult{}, err
	}
	result := CommandResult{Version: ContractVersion, SubscriberID: id, Unsubscribed: true}
	if err := saveResult(ctx, tx, request.RequestID, result); err != nil {
		return CommandResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return CommandResult{}, err
	}
	return result, nil
}

func (o *Owner) NotifyIncident(ctx context.Context, request NotificationRequest) (CommandResult, error) {
	return o.notify(ctx, "incident", request)
}
func (o *Owner) NotifyMaintenance(ctx context.Context, request NotificationRequest) (CommandResult, error) {
	return o.notify(ctx, "maintenance", request)
}
func (o *Owner) notify(ctx context.Context, kind string, request NotificationRequest) (CommandResult, error) {
	if err := validVersion(request.Version); err != nil {
		return CommandResult{}, err
	}
	if err := require("request id", request.RequestID); err != nil {
		return CommandResult{}, err
	}
	if err := require("event id", request.EventID); err != nil {
		return CommandResult{}, err
	}
	if err := require("subject", request.Subject); err != nil {
		return CommandResult{}, err
	}
	now := request.OccurredAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return CommandResult{}, err
	}
	defer tx.Rollback()
	if cached, ok, err := cachedResult(ctx, tx, request.RequestID); err != nil || ok {
		if err != nil {
			return CommandResult{}, err
		}
		return cached, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.id,s.email,p.unsubscribe_token
		FROM subscribers s LEFT JOIN subscriber_private_tokens p ON p.subscriber_id=s.id
		WHERE s.state='confirmed' ORDER BY s.id`)
	if err != nil {
		return CommandResult{}, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, email string
		var unsubscribeToken sql.NullString
		if err := rows.Scan(&id, &email, &unsubscribeToken); err != nil {
			return CommandResult{}, err
		}
		body := request.Body
		if strings.TrimSpace(request.UnsubscribeBaseURL) != "" && unsubscribeToken.Valid {
			body, err = bodyWithUnsubscribeURL(body, request.UnsubscribeBaseURL, unsubscribeToken.String)
			if err != nil {
				return CommandResult{}, err
			}
		}
		inserted, err := insertIntentIfAbsent(ctx, tx, id, request.EventID, "status."+kind, email, request.Subject, body, now)
		if err != nil {
			return CommandResult{}, err
		}
		if inserted {
			count++
		}
	}
	if err := rows.Err(); err != nil {
		return CommandResult{}, err
	}
	result := CommandResult{Version: ContractVersion, IntentCount: count}
	if err := saveResult(ctx, tx, request.RequestID, result); err != nil {
		return CommandResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return CommandResult{}, err
	}
	return result, nil
}

func bodyWithUnsubscribeURL(body, baseURL, token string) (string, error) {
	baseURL, err := normalizedUnsubscribeBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	parsed, _ := url.Parse(baseURL)
	query := parsed.Query()
	query.Set("token", token)
	parsed.RawQuery = query.Encode()
	unsubscribeURL := parsed.String()
	if strings.Contains(body, "{{unsubscribe_url}}") {
		return strings.ReplaceAll(body, "{{unsubscribe_url}}", unsubscribeURL), nil
	}
	return strings.TrimRight(body, "\r\n") + "\n\nUnsubscribe: " + unsubscribeURL, nil
}

func insertIntent(ctx context.Context, tx *sql.Tx, subscriberID, eventID, kind, recipient, subject, body string, now time.Time) error {
	_, err := insertIntentIfAbsent(ctx, tx, subscriberID, eventID, kind, recipient, subject, body, now)
	return err
}
func insertIntentIfAbsent(ctx context.Context, tx *sql.Tx, subscriberID, eventID, kind, recipient, subject, body string, now time.Time) (bool, error) {
	intentID := opaqueID("intent_", subscriberID+"\x00"+eventID+"\x00"+kind)
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO subscriber_outbox(intent_id,subscriber_id,event_id,kind,recipient,subject,body,status,created_at) VALUES (?,?,?,?,?,?,?,?,?)`, intentID, subscriberID, eventID, kind, recipient, subject, body, "pending", now.UnixNano())
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func (o *Owner) Projection(ctx context.Context) (Projection, error) {
	p := Projection{Version: ContractVersion}
	for _, pair := range []struct {
		query string
		dest  *int
	}{
		{`SELECT COUNT(*) FROM subscribers WHERE state='pending'`, &p.PendingCount}, {`SELECT COUNT(*) FROM subscribers WHERE state='confirmed'`, &p.ConfirmedCount}, {`SELECT COUNT(*) FROM subscribers WHERE state='unsubscribed'`, &p.UnsubscribedCount}, {`SELECT COUNT(*) FROM subscriber_outbox WHERE status='pending'`, &p.PendingIntentCount},
	} {
		if err := o.store.db.QueryRowContext(ctx, pair.query).Scan(pair.dest); err != nil {
			return Projection{}, err
		}
	}
	return p, nil
}

func (o *Owner) ClaimOutbox(ctx context.Context, request OutboxClaimRequest) (OutboxClaim, error) {
	if err := validVersion(request.Version); err != nil {
		return OutboxClaim{}, err
	}
	if err := require("worker id", request.WorkerID); err != nil {
		return OutboxClaim{}, err
	}
	if request.Limit < 1 || request.Limit > 100 {
		return OutboxClaim{}, errors.New("claim limit must be 1..100")
	}
	rows, err := o.store.db.QueryContext(ctx, `SELECT intent_id,kind,recipient,subject,body FROM subscriber_outbox WHERE status='pending' ORDER BY created_at,intent_id LIMIT ?`, request.Limit)
	if err != nil {
		return OutboxClaim{}, err
	}
	defer rows.Close()
	claim := OutboxClaim{Version: ContractVersion}
	for rows.Next() {
		var intent OutboxIntent
		intent.Version = ContractVersion
		if err := rows.Scan(&intent.IntentID, &intent.Kind, &intent.Recipient, &intent.Subject, &intent.Body); err != nil {
			return OutboxClaim{}, err
		}
		intent.IdempotencyKey = intent.IntentID
		claim.Intents = append(claim.Intents, intent)
	}
	return claim, rows.Err()
}

func (o *Owner) ApplyReceipt(ctx context.Context, receipt OutboxReceipt) (ReceiptApplyResult, error) {
	if err := validVersion(receipt.Version); err != nil {
		return ReceiptApplyResult{}, err
	}
	if err := require("intent id", receipt.IntentID); err != nil {
		return ReceiptApplyResult{}, err
	}
	if receipt.Status != ReceiptDelivered && receipt.Status != ReceiptFailed {
		return ReceiptApplyResult{}, errors.New("unsupported receipt status")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return ReceiptApplyResult{}, err
	}
	defer tx.Rollback()
	raw, err := msgpack.Marshal(receipt)
	if err != nil {
		return ReceiptApplyResult{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO subscriber_outbox_receipts(intent_id,receipt,recorded_at) VALUES (?,?,?)`, receipt.IntentID, raw, time.Now().UTC().UnixNano())
	if err != nil {
		return ReceiptApplyResult{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return ReceiptApplyResult{}, err
	}
	if changed == 0 {
		if err := tx.Commit(); err != nil {
			return ReceiptApplyResult{}, err
		}
		return ReceiptApplyResult{Version: ContractVersion}, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE subscriber_outbox SET status=? WHERE intent_id=? AND status='pending'`, string(receipt.Status), receipt.IntentID); err != nil {
		return ReceiptApplyResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ReceiptApplyResult{}, err
	}
	return ReceiptApplyResult{Version: ContractVersion, Applied: true}, nil
}
