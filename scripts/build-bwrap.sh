#!/usr/bin/env bash
# Builds the bwrap Kstack's Linux packages carry, from one pinned bubblewrap
# release, into src-tauri/linux/: bwrap itself and the tarball it was built
# from, which `make embedded-sources` bundles as bwrap's source. It fails when
# licenses/ is stale for that release (licenses/README.md).
#
# Run it on the machine that builds the bundle: bwrap links the glibc and
# libcap it finds.
#
# Usage: scripts/build-bwrap.sh   (needs curl, meson, ninja and libcap's headers)
set -euo pipefail

VERSION="0.13.0"
SHA256="4734237473c0e5d695e4e9034a34e43b2dbf5164655bd13fa59ae376b2b7a765"

if [ "$(uname -s)" != Linux ]; then
  echo "bwrap is Linux's alone" >&2
  exit 1
fi

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT_DIR="$ROOT/src-tauri/linux"
LICENSES="$ROOT/licenses"
TARBALL="bubblewrap-$VERSION.tar.xz"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

echo "→ fetching bubblewrap $VERSION"
curl -fsSL -o "$WORK/$TARBALL" "https://github.com/containers/bubblewrap/releases/download/v$VERSION/$TARBALL"
echo "$SHA256  $WORK/$TARBALL" | sha256sum -c --quiet -

tar -xJf "$WORK/$TARBALL" -C "$WORK"
SRC="$WORK/bubblewrap-$VERSION"

grep -qF -- "**Version:** $VERSION" "$LICENSES/THIRD_PARTY_NOTICES.md" ||
  { echo "licenses/THIRD_PARTY_NOTICES.md does not name bubblewrap $VERSION" >&2; exit 1; }
cmp -s "$SRC/COPYING" "$LICENSES/bubblewrap-LICENSE" ||
  { echo "bubblewrap $VERSION's COPYING differs from licenses/bubblewrap-LICENSE" >&2; exit 1; }

meson setup "$WORK/build" "$SRC" --buildtype=release \
  -Dselinux=disabled -Dman=disabled -Dtests=false \
  -Dbash_completion=disabled -Dzsh_completion=disabled
ninja -C "$WORK/build" bwrap

mkdir -p "$OUT_DIR"
install -m 0755 "$WORK/build/bwrap" "$OUT_DIR/bwrap"
install -m 0644 "$WORK/$TARBALL" "$OUT_DIR/$TARBALL"
echo "✓ $OUT_DIR/bwrap ($("$OUT_DIR/bwrap" --version))"
