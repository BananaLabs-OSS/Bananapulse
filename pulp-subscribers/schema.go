package main

import (
	"context"
	"database/sql"
)

type schemaExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type Store struct{ db *sql.DB }

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) DB() *sql.DB  { return s.db }

func migrate(ctx context.Context, db schemaExecutor) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS subscribers (id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE, confirmation_hash TEXT NOT NULL UNIQUE, unsubscribe_hash TEXT NOT NULL UNIQUE, state TEXT NOT NULL, created_at INTEGER NOT NULL, confirmed_at INTEGER)`,
		`CREATE TABLE IF NOT EXISTS subscriber_commands (request_id TEXT PRIMARY KEY, response BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS subscriber_resend_receipts (request_id TEXT PRIMARY KEY, payload_hash TEXT NOT NULL, response BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS subscriber_admin_receipts (request_id TEXT PRIMARY KEY, command_kind TEXT NOT NULL, payload_hash TEXT NOT NULL, response BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS subscriber_transition_receipts (command_id TEXT PRIMARY KEY, payload_hash TEXT NOT NULL, response BLOB NOT NULL, recorded_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS subscriber_delivery_config (config_key TEXT PRIMARY KEY, unsubscribe_base_url TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS subscriber_private_tokens (subscriber_id TEXT PRIMARY KEY, unsubscribe_token TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS subscriber_outbox (intent_id TEXT PRIMARY KEY, subscriber_id TEXT NOT NULL, event_id TEXT NOT NULL, kind TEXT NOT NULL, recipient TEXT NOT NULL, subject TEXT NOT NULL, body TEXT NOT NULL, status TEXT NOT NULL, created_at INTEGER NOT NULL, UNIQUE(subscriber_id, event_id, kind))`,
		`CREATE TABLE IF NOT EXISTS subscriber_outbox_receipts (intent_id TEXT PRIMARY KEY, receipt BLOB NOT NULL, recorded_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS subscriber_migration_receipts (request_id TEXT PRIMARY KEY, payload_hash TEXT NOT NULL, response BLOB NOT NULL, recorded_at INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS subscriber_outbox_pending ON subscriber_outbox(status, created_at)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
