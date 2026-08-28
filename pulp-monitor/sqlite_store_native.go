//go:build !wasip1

package main

import (
	"database/sql"
	_ "modernc.org/sqlite"
)

func newSQLiteEventStore(path string) (*sqliteEventStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return &sqliteEventStore{db: db}, nil
}
