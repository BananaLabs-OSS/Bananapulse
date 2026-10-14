//go:build !wasip1

package main

import (
	"database/sql"
	_ "modernc.org/sqlite"
)

func openDB(path string) (*sql.DB, error) { return sql.Open("sqlite", path) }
