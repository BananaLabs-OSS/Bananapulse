package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"
)

const FnDiscordConfigGet = "subscription-outbox.v1.discord.config.get"
const FnDiscordConfigSet = "subscription-outbox.v1.discord.config.set"

type DiscordConfig struct {
	Version    string `msgpack:"version" json:"version"`
	Enabled    bool   `msgpack:"enabled" json:"enabled"`
	Outages    bool   `msgpack:"outages" json:"outages"`
	Recoveries bool   `msgpack:"recoveries" json:"recoveries"`
}

func readDiscordConfig(ctx context.Context, tx *sql.Tx) (DiscordConfig, error) {
	c := DiscordConfig{Version: ContractVersion, Outages: true, Recoveries: true}
	err := tx.QueryRowContext(ctx, `SELECT enabled,outages,recoveries FROM discord_delivery_config WHERE config_key='default'`).Scan(&c.Enabled, &c.Outages, &c.Recoveries)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return c, err
}
func (o *Owner) DiscordConfig(ctx context.Context) (DiscordConfig, error) {
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return DiscordConfig{}, err
	}
	defer tx.Rollback()
	return readDiscordConfig(ctx, tx)
}
func (o *Owner) SetDiscordConfig(ctx context.Context, c DiscordConfig) (DiscordConfig, error) {
	if err := validVersion(c.Version); err != nil {
		return DiscordConfig{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.db.BeginTx(ctx, nil)
	if err != nil {
		return DiscordConfig{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO discord_delivery_config VALUES ('default',?,?,?) ON CONFLICT(config_key) DO UPDATE SET enabled=excluded.enabled,outages=excluded.outages,recoveries=excluded.recoveries`, c.Enabled, c.Outages, c.Recoveries); err != nil {
		return DiscordConfig{}, err
	}
	// Disabling a channel or event also cancels pending deliveries for it.
	if _, err = tx.ExecContext(ctx, `UPDATE subscriber_outbox SET status='cancelled' WHERE status='pending' AND kind LIKE 'discord.%' AND (?=0 OR (kind='discord.incident.opened' AND ?=0) OR (kind='discord.incident.resolved' AND ?=0))`, c.Enabled, c.Outages, c.Recoveries); err != nil {
		return DiscordConfig{}, err
	}
	return c, tx.Commit()
}
func queueDiscordTransitions(ctx context.Context, tx *sql.Tx, transitions []DomainTransition, base string) error {
	c, err := readDiscordConfig(ctx, tx)
	if err != nil {
		return err
	}
	if !c.Enabled {
		return nil
	}
	for _, t := range transitions {
		if !(t.Kind == "incident.opened" && c.Outages || t.Kind == "incident.resolved" && c.Recoveries) {
			continue
		}
		// Never include raw observation details or addresses in Discord messages.
		title := "Service incident"
		if t.Incident != nil {
			title = t.Incident.Title
		}
		label, color := "Outage started", 0xdc3545
		description := "The service is unavailable. View the incident for details."
		if t.Incident != nil && t.Incident.Severity != "major" {
			label, color = "Service incident", 0xffc107
			description = "A service issue has been reported. View the incident for details."
			if t.Incident.Severity == "minor" {
				label = "Service degraded"
				description = "The service is degraded. It may still be available. View the incident for details."
			}
		}
		if t.Kind == "incident.resolved" {
			label, color = "Recovered", 0x198754
			description = "The service has recovered."
			if t.Incident != nil && t.Incident.ResolvedAtUnix >= t.Incident.StartedAtUnix && t.Incident.StartedAtUnix > 0 {
				description += fmt.Sprintf(" Incident duration: %s.", time.Duration(t.Incident.ResolvedAtUnix-t.Incident.StartedAtUnix)*time.Second)
			}
		}
		runes := []rune(title)
		if len(runes) > 180 {
			title = string(runes[:180])
		}
		body, err := json.Marshal(map[string]any{"allowed_mentions": map[string]any{"parse": []string{}}, "embeds": []any{map[string]any{"title": label + ": " + title, "description": description, "color": color, "url": discordIncidentURL(base, t.EntityID)}}})
		if err != nil {
			return err
		}
		if _, err = insertIntentIfAbsent(ctx, tx, "discord-default", t.ID, "discord."+t.Kind, "discord", label, string(body), time.Unix(t.AtUnix, 0)); err != nil {
			return err
		}
	}
	return nil
}

func discordIncidentURL(base, id string) string {
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	u.Path = "/incident/" + id
	u.RawPath = "/incident/" + url.PathEscape(id)
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
