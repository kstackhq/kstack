#!/bin/sh
# Runs staticcheck over the sidecar once per GOOS, and over the live checks'
# build tags, then merges the runs. A single run reports code used only by
# another platform as unused; -merge reports U1000 only when every run agrees.
set -eu

root="$(cd "$(dirname "$0")/.." && pwd)"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# Built for the host: under GOOS=windows, `go tool` would build a Windows binary.
go build -C "$root/tools" -o "$work/staticcheck" honnef.co/go/tools/cmd/staticcheck

cd "$root/sidecar"
for goos in linux darwin windows; do
	GOOS=$goos "$work/staticcheck" -f binary ./... >"$work/$goos.bin"
done
for tag in livecache liveswitch; do
	"$work/staticcheck" -f binary -tags "$tag" ./internal/agent/catalog >"$work/$tag.bin"
done
"$work/staticcheck" -merge "$work"/*.bin
