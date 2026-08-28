package main

import (
	"context"
	"errors"
	"fmt"
)

const subscriberContractVersion = "subscription-outbox/v1"

type emailSender interface {
	// Send is a privileged host effect. Implementations must pass the stable
	// idempotency key to their delivery provider.
	Send(context.Context, emailIntent) error
}

type retryableDeliveryError interface {
	error
	Retryable() bool
}

type emailIntent struct {
	Version        string `msgpack:"version" json:"version"`
	IntentID       string `msgpack:"intent_id" json:"intent_id"`
	IdempotencyKey string `msgpack:"idempotency_key" json:"idempotency_key"`
	Kind           string `msgpack:"kind" json:"kind"`
	Recipient      string `msgpack:"recipient" json:"recipient"`
	Subject        string `msgpack:"subject" json:"subject"`
	Body           string `msgpack:"body" json:"body"`
}

type outboxClaimRequest struct {
	Version  string `msgpack:"version" json:"version"`
	WorkerID string `msgpack:"worker_id" json:"worker_id"`
	Limit    int    `msgpack:"limit" json:"limit"`
}

type outboxClaim struct {
	Version string        `msgpack:"version" json:"version"`
	Intents []emailIntent `msgpack:"intents" json:"intents"`
}

type outboxReceipt struct {
	Version  string `msgpack:"version" json:"version"`
	IntentID string `msgpack:"intent_id" json:"intent_id"`
	Status   string `msgpack:"status" json:"status"`
	Detail   string `msgpack:"detail,omitempty" json:"detail,omitempty"`
}

type receiptApplyResult struct {
	Version string `msgpack:"version" json:"version"`
	Applied bool   `msgpack:"applied" json:"applied"`
}

type drainResult struct {
	Claimed   int
	Delivered int
	Failed    int
}

type emailOutboxWorker struct {
	client *applicationClient
	sender emailSender
}

func newEmailOutboxWorker(client *applicationClient, sender emailSender) (*emailOutboxWorker, error) {
	if client == nil {
		return nil, fmt.Errorf("application client is required")
	}
	if sender == nil {
		return nil, fmt.Errorf("email sender is required")
	}
	return &emailOutboxWorker{client: client, sender: sender}, nil
}

// DrainOnce is the only place recipient data leaves the subscriber owner.
// Every attempted host effect is settled back to that owner as a durable
// receipt before the worker moves to the next intent.
func (w *emailOutboxWorker) DrainOnce(ctx context.Context, workerID string, limit int) (drainResult, error) {
	request := outboxClaimRequest{Version: subscriberContractVersion, WorkerID: workerID, Limit: limit}
	var claim outboxClaim
	if err := w.client.callRaw(ctx, eventEmailOutboxClaim, request, &claim); err != nil {
		return drainResult{}, err
	}
	result := drainResult{Claimed: len(claim.Intents)}
	for _, intent := range claim.Intents {
		receipt := outboxReceipt{
			Version:  subscriberContractVersion,
			IntentID: intent.IntentID,
			Status:   "delivered",
		}
		if err := w.sender.Send(ctx, intent); err != nil {
			result.Failed++
			var retryable retryableDeliveryError
			if errors.As(err, &retryable) && retryable.Retryable() {
				// No receipt means the owner keeps the intent pending. The next
				// claim retries with the same durable provider idempotency key.
				continue
			}
			receipt.Status = "failed"
			receipt.Detail = boundedDetail(err.Error())
		} else {
			result.Delivered++
		}
		var applied receiptApplyResult
		if err := w.client.callRaw(ctx, eventEmailReceiptApply, receipt, &applied); err != nil {
			return result, fmt.Errorf("apply receipt for %s: %w", intent.IntentID, err)
		}
		// Applied=false is the owner's explicit replay result. The first
		// receipt is durable; a repeated apply after a lost host response is
		// therefore already settled and must be treated as success.
	}
	return result, nil
}

func boundedDetail(value string) string {
	const max = 512
	if len(value) <= max {
		return value
	}
	return value[:max]
}
