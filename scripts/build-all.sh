#!/usr/bin/env bash
# Cross-compile PocketNAS release binaries into dist/ and emit SHA256SUMS.
#
# Usage:
#   VERSION=v0.5.0 scripts/build-all.sh
# VERSION defaults to "dev" when not set (e.g. the git tag in CI).
set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="${VERSION:-dev}"
DIST=dist
LDFLAGS="-s -w -X main.Version=${VERSION} -X pocket-nas/internal/files.Version=${VERSION}"

rm -rf "$DIST"
mkdir -p "$DIST"

build() {
    local goos="$1" goarch="$2" out="$3"
    echo "==> building ${out} (version ${VERSION})"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
        go build -trimpath -ldflags "$LDFLAGS" -o "$DIST/$out" ./cmd/pocket-nas
}

build linux   amd64 pocket-nas-linux-amd64
build linux   arm64 pocket-nas-linux-arm64
build windows amd64 pocket-nas-windows-amd64.exe

# systemd unit template ships alongside the binaries.
cp packaging/pocket-nas.service "$DIST/"

(cd "$DIST" && sha256sum pocket-nas-* > SHA256SUMS)

echo
echo "Release ${VERSION} artifacts:"
ls -la "$DIST"
cat "$DIST/SHA256SUMS"
