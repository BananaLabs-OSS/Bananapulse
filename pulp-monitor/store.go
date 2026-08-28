package main

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/vmihailenco/msgpack/v5"
)

type EventStore interface {
	Migrate(context.Context) error
	Load(context.Context) ([]Command, error)
	Append(context.Context, Command, CommandResult) error
}

type sqliteEventStore struct{ db *sql.DB }

const monitorEventsSchema = `CREATE TABLE IF NOT EXISTS monitor_events (
 revision INTEGER PRIMARY KEY,
 command_id TEXT NOT NULL UNIQUE,
 command_payload BLOB NOT NULL
)`

func (s *sqliteEventStore) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, monitorEventsSchema)
	return err
}

func (s *sqliteEventStore) Load(ctx context.Context) ([]Command, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT command_payload FROM monitor_events ORDER BY revision ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Command
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var command Command
		if err := msgpack.Unmarshal(raw, &command); err != nil {
			return nil, fmt.Errorf("decode monitor command: %w", err)
		}
		events = append(events, command)
	}
	return events, rows.Err()
}

func (s *sqliteEventStore) Append(ctx context.Context, command Command, result CommandResult) error {
	raw, err := msgpack.Marshal(command)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO monitor_events (revision, command_id, command_payload) VALUES (?, ?, ?)`, result.Revision, command.ID, raw)
	return err
}
