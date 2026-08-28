module github.com/BananaLabs-OSS/Bananapulse/pulp-host

go 1.25.6

require (
	github.com/BananaLabs-OSS/Pulp v0.0.0
	github.com/BananaLabs-OSS/Pulp-ext-sqlite v0.0.0
	github.com/jackc/pgx/v5 v5.7.6
	github.com/vmihailenco/msgpack/v5 v5.4.1
	modernc.org/sqlite v1.48.2
)

require (
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/tetratelabs/wazero v1.11.0 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.24.0 // indirect
	modernc.org/libc v1.70.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

replace github.com/BananaLabs-OSS/Pulp => ../../Pulp

replace github.com/BananaLabs-OSS/Pulp-ext-entropy => ../../Pulp-ext-entropy

replace github.com/BananaLabs-OSS/Pulp-ext-fs => ../../Pulp-ext-fs

replace github.com/BananaLabs-OSS/Pulp-ext-http => ../../Pulp-ext-http

replace github.com/BananaLabs-OSS/Pulp-ext-sqlite => ../../Pulp-ext-sqlite

replace github.com/BananaLabs-OSS/Pulp-ext-udp => ../../Pulp-ext-udp
