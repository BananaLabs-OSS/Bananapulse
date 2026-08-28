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
		`CREATE TABLE IF NOT EXISTS auth_identities (
			id TEXT PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			email_hash TEXT NOT NULL UNIQUE,
			role TEXT NOT NULL,
			state TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS auth_magic_link_challenges (
			id TEXT PRIMARY KEY,
			identity_id TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			issued_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			consumed_at INTEGER,
			FOREIGN KEY(identity_id) REFERENCES auth_identities(id)
		)`,
		`CREATE TABLE IF NOT EXISTS auth_sessions (
			id TEXT PRIMARY KEY,
			identity_id TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			issued_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			revoked_at INTEGER,
			FOREIGN KEY(identity_id) REFERENCES auth_identities(id)
		)`,
		`CREATE TABLE IF NOT EXISTS auth_api_tokens (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			scope TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			created_at INTEGER NOT NULL,
			expires_at INTEGER,
			revoked_at INTEGER,
			last_used_at INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS auth_source_credentials (
			id TEXT PRIMARY KEY,
			source_id TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			created_at INTEGER NOT NULL,
			expires_at INTEGER,
			revoked_at INTEGER,
			last_used_at INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS auth_commands (
			request_id TEXT PRIMARY KEY,
			command_name TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			response BLOB NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS auth_audit_events (
			id TEXT PRIMARY KEY,
			action TEXT NOT NULL,
			subject_type TEXT NOT NULL,
			subject_id TEXT,
			actor_id TEXT,
			outcome TEXT NOT NULL,
			occurred_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS auth_magic_link_open ON auth_magic_link_challenges(consumed_at, expires_at)`,
		`CREATE INDEX IF NOT EXISTS auth_session_active ON auth_sessions(revoked_at, expires_at)`,
		`CREATE INDEX IF NOT EXISTS auth_api_token_active ON auth_api_tokens(revoked_at, expires_at)`,
		`CREATE INDEX IF NOT EXISTS auth_source_credential_active ON auth_source_credentials(source_id, revoked_at, expires_at)`,
		`CREATE INDEX IF NOT EXISTS auth_audit_recent ON auth_audit_events(occurred_at DESC, id DESC)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
