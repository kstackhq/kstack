# Polyglot task entry point. The Go sidecar lives outside the JS world,
# so it doesn't belong in package.json scripts; this Makefile is the
# place where Go, Rust, and JS commands meet.

.PHONY: sidecar sidecar-dev bwrap pasta proto proto-go proto-rust test test-changed test-go test-rust test-js cover-go cover-js lint lint-go lint-rust lint-js lint-yaml lint-markers vet vet-go staticcheck-go vet-rust check-sidecar-deps check-go-fix check-cache check-switch prompts clean clean-data

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
	cd tools && go test ./...

# tauri-build fails without the bundled sidecar binary, so build it first.
test-rust: sidecar
	cd src-tauri && cargo test

test-js:
	pnpm test --run

# Only the tests this branch's changes touch (scripts/test-changed.sh). The
# check to run while working; CI runs every suite and measures coverage.
test-changed:
	bash scripts/test-changed.sh

# Rewrites the goldens that pin what the model reads: the system prompt and every
# tool's offer, per kind of run and machine (sidecar/internal/app/testdata/prompt/),
# and a question's context block (sidecar/internal/agent/chat/testdata/context.md).
# Run it after changing a prompt, an offer or their assembly, and review the diff:
# it is the model's view of the change, byte for byte where the cache depends on it.
prompts:
	cd sidecar && go test ./internal/app ./internal/agent/chat -run MatchesItsGolden -update

# Go coverage of this OS, per function. Not a gate: codecov/patch on the PR is.
# `go tool cover -html=sidecar/coverage.out` opens it line by line.
cover-go:
	cd sidecar && go test -coverpkg=./... -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

# Sends two turns to every model of every provider whose key is in the environment
# and prints whether the second read its prefix from the cache. It reaches the
# vendors and spends real tokens, so nothing runs it automatically: run it before
# a catalog change lands and paste its lines into the PR.
check-cache:
	cd sidecar && go test -tags livecache -count=1 -v -run TestEveryModelReadsItsSecondTurnFromTheCache ./internal/agent/catalog

# Walks one chat across every dialect — Anthropic, OpenAI, a Chat Completions
# vendor, Anthropic again — each step a real tool round over the steps before it,
# with the keys in the environment. It spends real tokens, so nothing runs it
# automatically: run it before a change to how a wire replays another's rows lands.
check-switch:
	cd sidecar && go test -tags liveswitch -count=1 -v -run TestAChatSwitchesAcrossEveryDialect ./internal/agent/catalog

# Frontend coverage, per file. Not a gate: codecov/patch on the PR is.
cover-js:
	pnpm vitest run --mode coverage

# Run every linter in the repo.
lint: lint-go lint-rust lint-js lint-yaml lint-markers

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

# Every YAML file, then the workflows and composite actions: actionlint
# type-checks them and shellchecks their `run:` blocks (when shellcheck is on
# PATH), zizmor audits them against .github/ci-config/zizmor.yml. zizmor's
# online audits run only when GH_TOKEN is set, as in CI.
lint-yaml:
	uv tool run yamllint@1.38.0 --strict .
	go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 -config-file .github/ci-config/actionlint.yaml
	uv tool run zizmor@1.31.1 --config .github/ci-config/zizmor.yml .

# Every coverage marker gives its reason.
lint-markers:
	sh scripts/check-coverage-markers.sh

# Static analysis (separate from formatting checks in `lint`).
vet: vet-go staticcheck-go vet-rust

# The live checks are behind tags no other target builds; vetting them here keeps
# them compiling.
vet-go: check-sidecar-deps check-go-fix
	cd sidecar && go vet ./... && go vet -tags livecache ./internal/agent/catalog && go vet -tags liveswitch ./internal/agent/catalog

# One run per GOOS and live-check tag, merged (scripts/staticcheck.sh).
staticcheck-go:
	sh scripts/staticcheck.sh

check-sidecar-deps:
	sh scripts/check-sidecar-deps.sh

# Fails if go fix's modernizers would rewrite the sidecar (scripts/check-go-fix.sh).
check-go-fix:
	sh scripts/check-go-fix.sh

vet-rust:
	cd src-tauri && cargo clippy --all-targets -- -D warnings

clean:
	rm -rf src-tauri/binaries
	rm -rf src-tauri/target
	cd sidecar && go clean -testcache

# Delete the dev app's data and cache directories (the database, chats,
# settings and mirror). Separate from `clean` so clearing build outputs never
# deletes a developer's chats.
clean-data:
	go run scripts/clean-dev-data.go
