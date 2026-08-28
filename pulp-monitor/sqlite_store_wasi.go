//go:build wasip1

package main

import (
	"database/sql"
	_ "github.com/BananaLabs-OSS/Fiber/pulp/sql"
)

func newSQLiteEventStore(_ string) (*sqliteEventStore, error) {
	db, err := sql.Open("pulp", "")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return &sqliteEventStore{db: db}, nil
}
