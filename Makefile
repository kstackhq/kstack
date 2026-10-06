# Polyglot task entry point. The Go sidecar lives outside the JS world,
# so it doesn't belong in package.json scripts; this Makefile is the
# place where Go, Rust, and JS commands meet.

.PHONY: sidecar sidecar-dev bwrap pasta proto proto-go proto-rust test test-changed test-go test-rust test-js cover-go cover-js lint lint-go lint-rust lint-js vet vet-go vet-rust check-cache check-switch clean

# Build the Go sidecar into src-tauri/binaries/ with the Tauri-required
# `<name>-<rust-host-triple>` filename. Tauri's externalBin picks it up.
sidecar:
	go run scripts/build-sidecar.go

# The dev build, and the only one that lets KSTACK_OAUTH_ISSUER and friends
# redirect the sidecar. `tauri dev` calls this; every release path calls
# `sidecar` and so builds untagged.
sidecar-dev:
	go run scripts/build-sidecar.go -tags debug

# Build the bwrap Kstack's Linux packages carry into src-tauri/linux/, from
# the bubblewrap release the script pins. Linux only.
bwrap:
	bash scripts/build-bwrap.sh

# Build the pasta Kstack's Linux packages carry into src-tauri/linux/, from the
# passt release the script pins. Linux only.
pasta:
	bash scripts/build-pasta.sh

# Regenerate the gRPC bindings from the shared repo-root proto/ for both
# languages. proto/ is the single source of truth (host <-> sidecar wire
# format); the Go bindings are committed, the Rust ones are produced by
# src-tauri/build.rs at compile time (using a vendored protoc, so plain Rust
# builds need no system install).
proto: proto-go proto-rust

# Go: protoc-gen-go + protoc-gen-go-grpc via the //go:generate directives in
# sidecar/grpc/authpb and sidecar/grpc/pokepb. One-time tooling for regenerating the committed
# Go bindings: install `protoc`, then `go install
# google.golang.org/protobuf/cmd/protoc-gen-go` and `go install
# google.golang.org/grpc/cmd/protoc-gen-go-grpc` (versions pinned in
# sidecar/tools.go). The Rust side needs none of this — its protoc is vendored.
proto-go:
	cd sidecar && go generate ./grpc/...

# Rust: bindings are emitted by src-tauri/build.rs on build; this forces a
# rebuild so any proto/codegen error surfaces from `make proto`.
proto-rust:
	cd src-tauri && cargo build

# Run every test suite in the repo.
test: test-go test-rust test-js

test-go:
	cd sidecar && go test ./...

# Rust integration test spawns the real sidecar binary, so build it first.
test-rust: sidecar
	cd src-tauri && cargo test

test-js:
	pnpm test --run

# Only the tests this branch's changes touch (scripts/test-changed.sh). The
# check to run while working; CI runs every suite and both coverage gates.
test-changed:
	bash scripts/test-changed.sh

# Go coverage gate. Runs the suite untagged and with `-tags debug` (the only build
# that compiles the environment overrides), merges the profiles, drops generated
# files, and fails below sidecar/scripts/coverage-threshold. Add `-- -report` for
# the per-file list of what is still uncovered.
#
# Measures what this platform builds: a `_windows.go` file is not compiled here, so
# run it on one OS (CI uses Linux) rather than comparing numbers across a matrix.
cover-go:
	cd sidecar && go run scripts/coverage.go $(ARGS)

# Sends two turns to every model of every provider whose key is in the environment
# and prints whether the second read its prefix from the cache. It reaches the
# vendors and spends real tokens, so nothing runs it automatically: run it before
# a catalog change lands and paste its lines into the PR.
check-cache:
	cd sidecar && go test -tags livecache -count=1 -v -run TestEveryModelReadsItsSecondTurnFromTheCache ./internal/catalog

# Walks one chat across every dialect — Anthropic, OpenAI, a Chat Completions
# vendor, Anthropic again — each step a real tool round over the steps before it,
# with the keys in the environment. It spends real tokens, so nothing runs it
# automatically: run it before a change to how a wire replays another's rows lands.
check-switch:
	cd sidecar && go test -tags liveswitch -count=1 -v -run TestAChatSwitchesAcrossEveryDialect ./internal/catalog

# Frontend coverage gate: fails below the line percentage in
# scripts/coverage-threshold. Excludes generated output and the test harness
# (vite.config.ts). The threshold is a ratchet — raise it when the number rises,
# in the change that raised it.
cover-js:
	pnpm vitest run --coverage --coverage.thresholds.lines=$$(cat scripts/coverage-threshold)

# Run every linter in the repo.
lint: lint-go lint-rust lint-js

# `gofmt -l` lists unformatted files; non-empty = lint failure.
lint-go:
	@cd sidecar && unformatted=$$(gofmt -l .); \
		if [ -n "$$unformatted" ]; then \
			echo "gofmt: files need formatting:"; echo "$$unformatted"; exit 1; \
		fi

lint-rust:
	cd src-tauri && cargo fmt --check

lint-js:
	pnpm lint

# Static analysis (separate from formatting checks in `lint`).
vet: vet-go vet-rust

# The live checks are behind tags no other target builds; vetting them here keeps
# them compiling.
vet-go:
	cd sidecar && go vet ./... && go vet -tags livecache ./internal/catalog && go vet -tags liveswitch ./internal/catalog

vet-rust:
	cd src-tauri && cargo clippy --all-targets -- -D warnings

clean:
	rm -rf src-tauri/binaries
	rm -rf src-tauri/target
	cd sidecar && go clean -testcache
