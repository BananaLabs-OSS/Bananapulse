//go:build wasip1

package main

import (
	"context"
	"database/sql"

	_ "github.com/BananaLabs-OSS/Fiber/pulp/sql"
)

func openPulpStore() (*Store, error) {
	db, err := sql.Open("pulp", "")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := migrate(context.Background(), db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}
