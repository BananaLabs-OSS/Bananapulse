package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

const monitorContractVersion = "monitor.v1"

func normalizedUnsubscribeBaseURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return "", errors.New("unsubscribe base URL must be an absolute http(s) URL")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("unsubscribe base URL must not contain user information or a fragment")
	}
	if parsed.Query().Has("token") {
		return "", errors.New("unsubscribe base URL must not contain a token")
	}
	return parsed.String(), nil
}

func (o *Owner) SetDeliveryConfig(ctx context.Context, request DeliveryConfigSetRequest) (DeliveryConfigSetResult, error) {
	if err := validVersion(request.Version); err != nil {
		return DeliveryConfigSetResult{}, err
	}
	if err := require("request id", request.RequestID); err != nil {
		return DeliveryConfigSetResult{}, err
	}
	baseURL, err := normalizedUnsubscribeBaseURL(request.UnsubscribeBaseURL)
	if err != nil {
		return DeliveryConfigSetResult{}, err
	}
	request.UnsubscribeBaseURL = baseURL
	payloadHash, err := commandPayloadHash(request)
	if err != nil {
		return DeliveryConfigSetResult{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return DeliveryConfigSetResult{}, err
	}
	defer tx.Rollback()
	var priorKind, priorHash string
	var priorRaw []byte
	err = tx.QueryRowContext(ctx, `SELECT command_kind,payload_hash,response FROM subscriber_admin_receipts WHERE request_id=?`, request.RequestID).
		Scan(&priorKind, &priorHash, &priorRaw)
	if err == nil {
		if priorKind != "delivery.config.set" || priorHash != payloadHash {
			return DeliveryConfigSetResult{}, errors.New("delivery config request id was reused with a different command")
		}
		var prior DeliveryConfigSetResult
		if err := msgpack.Unmarshal(priorRaw, &prior); err != nil {
			return DeliveryConfigSetResult{}, err
		}
		return prior, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DeliveryConfigSetResult{}, err
	}
	current := ""
	err = tx.QueryRowContext(ctx, `SELECT unsubscribe_base_url FROM subscriber_delivery_config WHERE config_key='default'`).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return DeliveryConfigSetResult{}, err
	}
	result := DeliveryConfigSetResult{
		Version: ContractVersion, Applied: current != baseURL, UnsubscribeBaseURL: baseURL,
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO subscriber_delivery_config(config_key,unsubscribe_base_url) VALUES ('default',?)
		ON CONFLICT(config_key) DO UPDATE SET unsubscribe_base_url=excluded.unsubscribe_base_url`, baseURL); err != nil {
		return DeliveryConfigSetResult{}, err
	}
	raw, err := msgpack.Marshal(result)
	if err != nil {
		return DeliveryConfigSetResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO subscriber_admin_receipts(request_id,command_kind,payload_hash,response) VALUES (?,?,?,?)`,
		request.RequestID, "delivery.config.set", payloadHash, raw); err != nil {
		return DeliveryConfigSetResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return DeliveryConfigSetResult{}, err
	}
	return result, nil
}

func cachedTransitionResult(ctx context.Context, tx *sql.Tx, commandID, payloadHash string) (TransitionApplyResult, bool, error) {
	var priorHash string
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT payload_hash,response FROM subscriber_transition_receipts WHERE command_id=?`, commandID).
		Scan(&priorHash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return TransitionApplyResult{}, false, nil
	}
	if err != nil {
		return TransitionApplyResult{}, false, err
	}
	if priorHash != payloadHash {
		return TransitionApplyResult{}, false, errors.New("monitor command id was reused with a different result")
	}
	var result TransitionApplyResult
	if err := msgpack.Unmarshal(raw, &result); err != nil {
		return TransitionApplyResult{}, false, err
	}
	return result, true, nil
}

func saveTransitionResult(ctx context.Context, tx *sql.Tx, commandID, payloadHash string, result TransitionApplyResult) error {
	raw, err := msgpack.Marshal(result)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO subscriber_transition_receipts(command_id,payload_hash,response,recorded_at) VALUES (?,?,?,?)`,
		commandID, payloadHash, raw, time.Now().UTC().UnixNano())
	return err
}

func validateTransition(transition DomainTransition) error {
	switch transition.Kind {
	case "incident.opened", "incident.updated", "incident.resolved", "incident.deleted",
		"maintenance.created", "maintenance.updated", "maintenance.cancelled", "maintenance.deleted":
	default:
		return fmt.Errorf("unsupported monitor transition kind %q", transition.Kind)
	}
	if err := require("transition entity id", transition.EntityID); err != nil {
		return err
	}
	if err := require("transition id", transition.ID); err != nil {
		return err
	}
	if strings.ContainsAny(transition.ID+transition.EntityID+transition.ComponentID+transition.Status+transition.Severity, "\r\n") {
		return errors.New("monitor transition metadata must not contain line breaks")
	}
	for _, componentID := range transition.AffectedComponentIDs {
		if strings.ContainsAny(componentID, "\r\n") {
			return errors.New("affected component ids must not contain line breaks")
		}
	}
	return nil
}

func subjectValue(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "status event"
	}
	const maximum = 160
	runes := []rune(value)
	if len(runes) > maximum {
		return string(runes[:maximum])
	}
	return value
}

func transitionNotification(transition DomainTransition) (string, string) {
	label := strings.ReplaceAll(transition.Kind, ".", " ")
	entityLabel := transition.EntityID
	if transition.Incident != nil && strings.TrimSpace(transition.Incident.Title) != "" {
		entityLabel = transition.Incident.Title
	}
	if transition.Maintenance != nil && strings.TrimSpace(transition.Maintenance.Title) != "" {
		entityLabel = transition.Maintenance.Title
	}
	subject := strings.ToUpper(label[:1]) + label[1:] + ": " + subjectValue(entityLabel)
	lines := []string{
		"Status event: " + label,
		"Entity: " + transition.EntityID,
	}
	affected := transition.AffectedComponentIDs
	if len(affected) == 0 && transition.ComponentID != "" {
		affected = []string{transition.ComponentID}
	}
	if len(affected) != 0 {
		lines = append(lines, "Affected components: "+strings.Join(affected, ", "))
	}
	if transition.Status != "" {
		lines = append(lines, "Status: "+transition.Status)
	}
	if transition.Severity != "" {
		lines = append(lines, "Severity: "+transition.Severity)
	}
	if transition.Incident != nil && strings.TrimSpace(transition.Incident.Summary) != "" {
		lines = append(lines, "", transition.Incident.Summary)
	}
	if transition.IncidentUpdate != nil {
		if strings.TrimSpace(transition.IncidentUpdate.Label) != "" {
			lines = append(lines, "", transition.IncidentUpdate.Label)
		}
		if strings.TrimSpace(transition.IncidentUpdate.Body) != "" {
			lines = append(lines, transition.IncidentUpdate.Body)
		}
	}
	if transition.Maintenance != nil {
		if strings.TrimSpace(transition.Maintenance.Summary) != "" {
			lines = append(lines, "", transition.Maintenance.Summary)
		}
		if transition.Maintenance.ScheduledStartUnix != 0 {
			lines = append(lines, "Scheduled start: "+time.Unix(transition.Maintenance.ScheduledStartUnix, 0).UTC().Format(time.RFC3339))
		}
		if transition.Maintenance.ScheduledEndUnix != 0 {
			lines = append(lines, "Scheduled end: "+time.Unix(transition.Maintenance.ScheduledEndUnix, 0).UTC().Format(time.RFC3339))
		}
	}
	if transition.AtUnix != 0 {
		lines = append(lines, "Occurred at: "+time.Unix(transition.AtUnix, 0).UTC().Format(time.RFC3339))
	}
	lines = append(lines, "", "Unsubscribe: {{unsubscribe_url}}")
	return subject, strings.Join(lines, "\n")
}

func (o *Owner) ApplyTransitionRaw(ctx context.Context, raw []byte) (TransitionApplyResult, error) {
	var request MonitorCommandResult
	if err := msgpack.Unmarshal(raw, &request); err != nil {
		return TransitionApplyResult{}, err
	}
	return o.applyTransition(ctx, request)
}

func (o *Owner) ApplyTransition(ctx context.Context, request MonitorCommandResult) (TransitionApplyResult, error) {
	return o.applyTransition(ctx, request)
}

func (o *Owner) applyTransition(ctx context.Context, request MonitorCommandResult) (TransitionApplyResult, error) {
	if request.Version != monitorContractVersion {
		return TransitionApplyResult{}, fmt.Errorf("unsupported monitor contract %q", request.Version)
	}
	if err := require("monitor command id", request.CommandID); err != nil {
		return TransitionApplyResult{}, err
	}
	canonical := request
	canonical.Deduped = false
	payload, err := msgpack.Marshal(canonical)
	if err != nil {
		return TransitionApplyResult{}, err
	}
	payloadHash := secretHash(string(payload))
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return TransitionApplyResult{}, err
	}
	defer tx.Rollback()
	if cached, ok, err := cachedTransitionResult(ctx, tx, request.CommandID, payloadHash); err != nil || ok {
		if ok {
			cached.IntentCount = 0
			cached.Suppressed = true
		}
		return cached, err
	}
	result := TransitionApplyResult{
		Version: ContractVersion, MonitorCommand: request.CommandID,
		TransitionCount: len(request.Transitions),
	}
	if len(request.Transitions) == 0 {
		result.Suppressed = true
		if err := saveTransitionResult(ctx, tx, request.CommandID, payloadHash, result); err != nil {
			return TransitionApplyResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return TransitionApplyResult{}, err
		}
		return result, nil
	}
	for _, transition := range request.Transitions {
		if err := validateTransition(transition); err != nil {
			return TransitionApplyResult{}, err
		}
		expectedID := request.CommandID + "/" + transition.Kind + "/" + transition.EntityID
		if transition.ID != expectedID {
			return TransitionApplyResult{}, fmt.Errorf("transition id %q does not match %q", transition.ID, expectedID)
		}
	}
	var unsubscribeBaseURL string
	err = tx.QueryRowContext(ctx, `SELECT unsubscribe_base_url FROM subscriber_delivery_config WHERE config_key='default'`).Scan(&unsubscribeBaseURL)
	if errors.Is(err, sql.ErrNoRows) {
		return TransitionApplyResult{}, errors.New("subscriber delivery config is required before applying monitor transitions")
	}
	if err != nil {
		return TransitionApplyResult{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.id,s.email,p.unsubscribe_token
		FROM subscribers s LEFT JOIN subscriber_private_tokens p ON p.subscriber_id=s.id
		WHERE s.state='confirmed' ORDER BY s.id`)
	if err != nil {
		return TransitionApplyResult{}, err
	}
	type recipient struct {
		id, email        string
		unsubscribeToken sql.NullString
	}
	var recipients []recipient
	for rows.Next() {
		var value recipient
		if err := rows.Scan(&value.id, &value.email, &value.unsubscribeToken); err != nil {
			rows.Close()
			return TransitionApplyResult{}, err
		}
		recipients = append(recipients, value)
	}
	if err := rows.Close(); err != nil {
		return TransitionApplyResult{}, err
	}
	if err := rows.Err(); err != nil {
		return TransitionApplyResult{}, err
	}
	for _, recipient := range recipients {
		if !recipient.unsubscribeToken.Valid || recipient.unsubscribeToken.String == "" {
			return TransitionApplyResult{}, fmt.Errorf("confirmed subscriber %q has no private unsubscribe token", recipient.id)
		}
	}
	for _, transition := range request.Transitions {
		subject, template := transitionNotification(transition)
		eventID := "monitor:" + request.CommandID + ":" + transition.ID
		for _, recipient := range recipients {
			body, err := bodyWithUnsubscribeURL(template, unsubscribeBaseURL, recipient.unsubscribeToken.String)
			if err != nil {
				return TransitionApplyResult{}, err
			}
			inserted, err := insertIntentIfAbsent(ctx, tx, recipient.id, eventID, "status."+transition.Kind, recipient.email, subject, body, time.Unix(transition.AtUnix, 0).UTC())
			if err != nil {
				return TransitionApplyResult{}, err
			}
			if inserted {
				result.IntentCount++
			}
		}
	}
	if err := saveTransitionResult(ctx, tx, request.CommandID, payloadHash, result); err != nil {
		return TransitionApplyResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return TransitionApplyResult{}, err
	}
	return result, nil
}
