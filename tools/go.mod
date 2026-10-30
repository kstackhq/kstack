module github.com/kstackhq/kstack/tools

go 1.26.10

tool (
	golang.org/x/vuln/cmd/govulncheck
	honnef.co/go/tools/cmd/staticcheck
)

require (
	golang.org/x/mod v0.39.0
	golang.org/x/tools v0.49.0
)

require (
	github.com/BurntSushi/toml v1.4.1-0.20240526193622-a339e1f7089c // indirect
	golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/telemetry v0.0.0-20260811182544-a038080d80e5 // indirect
	golang.org/x/vuln v1.7.0 // indirect
	honnef.co/go/tools v0.8.1 // indirect
)
