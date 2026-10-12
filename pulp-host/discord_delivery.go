package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const discordSettingsEvent = "bananapulse.host.discord.settings.v1"
const discordTestEvent = "bananapulse.host.discord.test.v1"
const discordConfigGetProvider = "subscription-outbox.v1.discord.config.get"
const discordConfigSetProvider = "subscription-outbox.v1.discord.config.set"

type discordPreferences struct {
	Version    string `json:"version" msgpack:"version"`
	Enabled    bool   `json:"enabled" msgpack:"enabled"`
	Outages    bool   `json:"outages" msgpack:"outages"`
	Recoveries bool   `json:"recoveries" msgpack:"recoveries"`
}
type discordSettingsRequest struct {
	Action     string `json:"action"`
	WebhookURL string `json:"webhookUrl"`
	Remove     bool   `json:"remove"`
	Enabled    bool   `json:"enabled"`
	Outages    bool   `json:"outages"`
	Recoveries bool   `json:"recoveries"`
}
type discordSettingsView struct {
	Configured bool `json:"configured"`
	Enabled    bool `json:"enabled"`
	Outages    bool `json:"outages"`
	Recoveries bool `json:"recoveries"`
}
type discordDelivery struct {
	db          *sql.DB
	client      *applicationClient
	aead        cipher.AEAD
	http        *http.Client
	mu          sync.Mutex
	nextAttempt time.Time
	lastTest    time.Time
}

var discordWebhookPath = regexp.MustCompile(`^/api(?:/v[0-9]+)?/webhooks/[0-9]{15,22}/[A-Za-z0-9_-]{20,200}$`)

func validateDiscordWebhook(value string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.Scheme != "https" || u.Host != "discord.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !discordWebhookPath.MatchString(u.Path) {
		return "", errors.New("use a Discord HTTPS webhook URL from discord.com")
	}
	return u.String(), nil
}
func newDiscordDelivery(root string, client *applicationClient) (*discordDelivery, error) {
	keyPath := filepath.Join(root, "discord-webhook.key")
	key, err := os.ReadFile(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		f, e := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return nil, e
		}
		_, err = f.Write(key)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("invalid Discord encryption key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(root, "discord-webhook.sqlite")
	f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS discord_secret (id INTEGER PRIMARY KEY CHECK(id=1), encrypted BLOB NOT NULL)`); err != nil {
		db.Close()
		return nil, err
	}
	return &discordDelivery{db: db, client: client, aead: aead, http: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (d *discordDelivery) secret(ctx context.Context) (string, error) {
	var raw []byte
	err := d.db.QueryRowContext(ctx, `SELECT encrypted FROM discord_secret WHERE id=1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	n := d.aead.NonceSize()
	if len(raw) < n {
		return "", errors.New("invalid encrypted webhook")
	}
	plain, err := d.aead.Open(nil, raw[:n], raw[n:], []byte("bananapulse-discord-v1"))
	return string(plain), err
}
func (d *discordDelivery) settings(ctx context.Context, r discordSettingsRequest) (discordSettingsView, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if r.Action != "get" && r.Action != "save" {
		return discordSettingsView{}, &bridgeRequestError{message: "unknown Discord settings action"}
	}
	secret, err := d.secret(ctx)
	if err != nil {
		return discordSettingsView{}, err
	}
	if r.Action == "save" {
		if r.Remove {
			secret = ""
		} else if strings.TrimSpace(r.WebhookURL) != "" {
			secret, err = validateDiscordWebhook(r.WebhookURL)
			if err != nil {
				return discordSettingsView{}, &bridgeRequestError{message: err.Error()}
			}
		}
		if r.Enabled && secret == "" && !r.Remove {
			return discordSettingsView{}, &bridgeRequestError{message: "save a webhook before enabling notifications"}
		}
		if r.Remove {
			r.Enabled = false
		}
		if r.Remove {
			_, err = d.db.ExecContext(ctx, `DELETE FROM discord_secret WHERE id=1`)
		} else if r.WebhookURL != "" {
			nonce := make([]byte, d.aead.NonceSize())
			if _, err = rand.Read(nonce); err == nil {
				encrypted := d.aead.Seal(nonce, nonce, []byte(secret), []byte("bananapulse-discord-v1"))
				_, err = d.db.ExecContext(ctx, `INSERT INTO discord_secret VALUES (1,?) ON CONFLICT(id) DO UPDATE SET encrypted=excluded.encrypted`, encrypted)
			}
		}
		if err != nil {
			return discordSettingsView{}, err
		}
		prefs := discordPreferences{Version: subscriberContractVersion, Enabled: r.Enabled, Outages: r.Outages, Recoveries: r.Recoveries}
		if err = d.client.callProviderRaw(ctx, subscriberOwnerCell, discordConfigSetProvider, prefs, &prefs); err != nil {
			return discordSettingsView{}, err
		}
	}
	var p discordPreferences
	if err = d.client.callProviderRaw(ctx, subscriberOwnerCell, discordConfigGetProvider, map[string]any{}, &p); err != nil {
		return discordSettingsView{}, err
	}
	return discordSettingsView{Configured: secret != "", Enabled: p.Enabled, Outages: p.Outages, Recoveries: p.Recoveries}, nil
}

type discordSendError struct{ retry bool }

func (e discordSendError) Error() string   { return "Discord delivery failed" }
func (e discordSendError) Retryable() bool { return e.retry }
func (d *discordDelivery) Send(ctx context.Context, intent emailIntent) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if time.Now().Before(d.nextAttempt) {
		return discordSendError{retry: true}
	}
	secret, err := d.secret(ctx)
	if err != nil || secret == "" {
		return discordSendError{retry: true}
	}
	target, err := validateDiscordWebhook(secret)
	if err != nil {
		return discordSendError{}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target+"?wait=true", bytes.NewBufferString(intent.Body))
	if err != nil {
		return discordSendError{}
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := d.http.Do(request)
	if err != nil {
		d.nextAttempt = time.Now().Add(30 * time.Second)
		return discordSendError{retry: true}
	}
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	if response.StatusCode == 429 {
		var rate struct {
			RetryAfter float64 `json:"retry_after"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&rate)
		delay := time.Duration(rate.RetryAfter * float64(time.Second))
		if delay < time.Second {
			delay = time.Second
		}
		if delay > 24*time.Hour {
			delay = 24 * time.Hour
		}
		d.nextAttempt = time.Now().Add(delay)
		return discordSendError{retry: true}
	}
	if response.StatusCode >= 500 {
		d.nextAttempt = time.Now().Add(30 * time.Second)
		return discordSendError{retry: true}
	}
	return discordSendError{}
}
func (d *discordDelivery) test(ctx context.Context) error {
	d.mu.Lock()
	if time.Since(d.lastTest) < 10*time.Second {
		d.mu.Unlock()
		return &bridgeDomainError{Status: 429, Code: "rate_limited", Message: "Wait a few seconds before testing again."}
	}
	d.lastTest = time.Now()
	d.mu.Unlock()
	return d.Send(ctx, emailIntent{Body: `{"content":"Bananapulse test: Discord notifications are connected.","allowed_mentions":{"parse":[]}}`})
}
func (b *httpBridge) discordHTTP(ctx context.Context, event string, r *http.Request, w http.ResponseWriter) error {
	if !b.families.authAdmin || b.token == "" || b.discord == nil {
		return disabledBridgeFamily()
	}
	if event == discordTestEvent {
		if err := b.discord.test(ctx); err != nil {
			var domain *bridgeDomainError
			if errors.As(err, &domain) {
				return domain
			}
			return &bridgeDomainError{Status: 502, Code: "discord_failed", Message: "Discord test failed. Check the saved webhook and try again."}
		}
		writeBridgeJSON(w, http.StatusOK, map[string]bool{"sent": true})
		return nil
	}
	var request discordSettingsRequest
	if err := decodeBridgeJSON(r, &request, false); err != nil {
		return err
	}
	result, err := b.discord.settings(ctx, request)
	if err != nil {
		return err
	}
	writeBridgeJSON(w, http.StatusOK, result)
	return nil
}
