package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

const (
	resendAPIKeyEnv    = "RESEND_API_KEY"
	resendFromEmailEnv = "RESEND_FROM_EMAIL"
	resendFromNameEnv  = "RESEND_FROM_NAME"
	resendEndpoint     = "https://api.resend.com/emails"
)

type resendConfig struct {
	APIKey    string
	FromEmail string
	FromName  string
	Endpoint  string
}

// resendConfigFromEnv is intentionally the only delivery configuration read.
// It reads process environment values directly, never dotenv files, and no
// caller logs or returns the API key.
func resendConfigFromEnv() (resendConfig, bool) {
	config := resendConfig{
		APIKey:    strings.TrimSpace(os.Getenv(resendAPIKeyEnv)),
		FromEmail: strings.TrimSpace(os.Getenv(resendFromEmailEnv)),
		FromName:  strings.TrimSpace(os.Getenv(resendFromNameEnv)),
		Endpoint:  resendEndpoint,
	}
	return config, config.APIKey != "" && config.FromEmail != ""
}

type resendSender struct {
	apiKey string
	from   string
	url    string
	client *http.Client
}

type deliveryError struct {
	err       error
	retryable bool
}

func (e *deliveryError) Error() string   { return e.err.Error() }
func (e *deliveryError) Unwrap() error   { return e.err }
func (e *deliveryError) Retryable() bool { return e.retryable }

func newResendSender(config resendConfig) (*resendSender, error) {
	if config.APIKey == "" || config.FromEmail == "" {
		return nil, errors.New("Resend API key and from email are required")
	}
	endpoint := config.Endpoint
	if endpoint == "" {
		endpoint = resendEndpoint
	}
	from := config.FromEmail
	if config.FromName != "" {
		from = fmt.Sprintf("%s <%s>", config.FromName, config.FromEmail)
	}
	return &resendSender{
		apiKey: config.APIKey,
		from:   from,
		url:    endpoint,
		client: &http.Client{},
	}, nil
}

type resendMessage struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
}

func (s *resendSender) Send(ctx context.Context, intent emailIntent) error {
	if intent.IdempotencyKey == "" || intent.Recipient == "" || intent.Subject == "" {
		return errors.New("email intent is missing delivery fields")
	}
	payload, err := json.Marshal(resendMessage{
		From: s.from, To: []string{intent.Recipient}, Subject: intent.Subject, Text: intent.Body,
	})
	if err != nil {
		return fmt.Errorf("encode Resend message: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create Resend request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+s.apiKey)
	request.Header.Set("Content-Type", "application/json")
	// This is the durable owner's idempotency key, not a host-generated retry
	// token. Replays therefore remain one logical provider delivery.
	request.Header.Set("Idempotency-Key", intent.IdempotencyKey)
	response, err := s.client.Do(request)
	if err != nil {
		return &deliveryError{err: fmt.Errorf("send Resend email: %w", err), retryable: true}
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		retryable := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError
		return &deliveryError{
			err: fmt.Errorf("Resend send failed: status %d", response.StatusCode), retryable: retryable,
		}
	}
	return nil
}
