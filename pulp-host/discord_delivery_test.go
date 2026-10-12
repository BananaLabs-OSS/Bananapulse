package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type discordTransport func(*http.Request) (*http.Response, error)

func (f discordTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const testWebhook = "https://discord.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz0123456789"

func TestDiscordRejectsSSRFAndCredentialLeakingURLs(t *testing.T) {
	for _, u := range []string{"http://discord.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz", "https://127.0.0.1/webhook", "https://discord.com.evil.test/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz", testWebhook + "?redirect=evil", testWebhook + "#fragment", strings.Replace(testWebhook, "discord.com", "user@discord.com", 1)} {
		if _, err := validateDiscordWebhook(u); err == nil {
			t.Fatalf("accepted %s", u)
		}
	}
	if _, err := validateDiscordWebhook(testWebhook); err != nil {
		t.Fatal(err)
	}
}
func TestDiscordSecretIsEncryptedPersistentAndNotProjected(t *testing.T) {
	root := t.TempDir()
	d, err := newDiscordDelivery(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, d.aead.NonceSize())
	encrypted := d.aead.Seal(nonce, nonce, []byte(testWebhook), []byte("bananapulse-discord-v1"))
	if _, err = d.db.Exec(`INSERT INTO discord_secret VALUES (1,?)`, encrypted); err != nil {
		t.Fatal(err)
	}
	d.db.Close()
	raw, err := os.ReadFile(filepath.Join(root, "discord-webhook.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "abcdefghijklmnopqrstuvwxyz") {
		t.Fatal("plaintext secret persisted")
	}
	d, err = newDiscordDelivery(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d.db.Close()
	got, err := d.secret(context.Background())
	if err != nil || got != testWebhook {
		t.Fatalf("secret reload failed: %v", err)
	}
	view, _ := json.Marshal(discordSettingsView{Configured: true})
	if strings.Contains(string(view), "webhookUrl") || strings.Contains(string(view), "token") {
		t.Fatal("secret exposed")
	}
}
func TestDiscordDeliveryHandlesRateLimitsWithoutFollowingRedirects(t *testing.T) {
	d, err := newDiscordDelivery(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d.db.Close()
	nonce := make([]byte, d.aead.NonceSize())
	encrypted := d.aead.Seal(nonce, nonce, []byte(testWebhook), []byte("bananapulse-discord-v1"))
	d.db.Exec(`INSERT INTO discord_secret VALUES (1,?)`, encrypted)
	calls := 0
	d.http.Transport = discordTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "discord.com" || r.URL.Query().Get("wait") != "true" {
			t.Fatal("wrong destination")
		}
		return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(`{"retry_after":30}`)), Header: make(http.Header)}, nil
	})
	err = d.Send(context.Background(), emailIntent{Body: `{"content":"test"}`})
	if e, ok := err.(retryableDeliveryError); !ok || !e.Retryable() {
		t.Fatal("429 did not retry")
	}
	d.Send(context.Background(), emailIntent{Body: `{}`})
	if calls != 1 {
		t.Fatal("ignored retry-after")
	}
	d.nextAttempt = time.Time{}
	d.http.Transport = discordTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})
	err = d.Send(context.Background(), emailIntent{Body: `{}`})
	if e, ok := err.(retryableDeliveryError); !ok || e.Retryable() {
		t.Fatal("redirect must be permanent failure")
	}
}
