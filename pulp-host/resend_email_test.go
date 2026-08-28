package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResendSenderUsesDurableIntentIdempotencyKey(t *testing.T) {
	var received resendMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Fatal("Resend authorization header is missing")
		}
		if r.Header.Get("Idempotency-Key") != "owner-intent-1" {
			t.Fatal("durable owner idempotency key was not forwarded")
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode Resend request: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)

	sender, err := newResendSender(resendConfig{
		APIKey: "test-api-key", FromEmail: "status@example.test", FromName: "Bananapulse", Endpoint: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.Send(context.Background(), emailIntent{
		IntentID: "intent-1", IdempotencyKey: "owner-intent-1", Recipient: "recipient@example.test", Subject: "Subject", Body: "Body",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if received.From != "Bananapulse <status@example.test>" || len(received.To) != 1 || received.To[0] != "recipient@example.test" {
		t.Fatalf("Resend payload = %#v", received)
	}
}

func TestResendSenderRejectsFailedDelivery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	sender, err := newResendSender(resendConfig{APIKey: "test-api-key", FromEmail: "status@example.test", Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.Send(context.Background(), emailIntent{
		IdempotencyKey: "owner-intent-1", Recipient: "recipient@example.test", Subject: "Subject",
	}); err == nil {
		t.Fatal("expected a failed provider delivery")
	}
}

func TestResendSenderClassifiesOnlyTransientProviderFailuresForRetry(t *testing.T) {
	for _, test := range []struct {
		status    int
		retryable bool
	}{
		{status: http.StatusBadRequest, retryable: false},
		{status: http.StatusTooManyRequests, retryable: true},
		{status: http.StatusBadGateway, retryable: true},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(test.status)
		}))
		sender, err := newResendSender(resendConfig{
			APIKey: "test-api-key", FromEmail: "status@example.test", Endpoint: server.URL,
		})
		if err != nil {
			t.Fatal(err)
		}
		err = sender.Send(context.Background(), emailIntent{
			IdempotencyKey: "owner-intent-1", Recipient: "recipient@example.test", Subject: "Subject",
		})
		server.Close()
		var classified retryableDeliveryError
		if !errors.As(err, &classified) || classified.Retryable() != test.retryable {
			t.Fatalf("status %d retry classification = %v, want %v (error %v)", test.status, classified.Retryable(), test.retryable, err)
		}
	}
}
