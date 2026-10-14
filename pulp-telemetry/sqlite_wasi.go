//go:build wasip1

package main

import (
	"database/sql"
	_ "github.com/BananaLabs-OSS/Fiber/pulp/sql"
)

func openDB(_ string) (*sql.DB, error) { return sql.Open("pulp", "") }
