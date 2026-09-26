#!/usr/bin/env sh
# Builds the web UI, embeds it in the Go binary and writes ./dist/ferry[.exe].
# Usage: scripts/build.sh [version] ; cross-compile with GOOS/GOARCH env vars.
set -eu
cd "$(dirname "$0")/.."
VERSION="${1:-$(git describe --tags --always 2>/dev/null || echo dev)}"
(cd web && npm ci --no-audit --no-fund && npm run build)
find backend/internal/server/webui -mindepth 1 ! -name .gitkeep -exec rm -rf {} + 2>/dev/null || true
cp -r web/dist/. backend/internal/server/webui/
EXT=""; [ "$(go env GOOS)" = "windows" ] && EXT=".exe"
mkdir -p dist
(cd backend && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "../dist/ferry$EXT" ./cmd/ferry)
[ -f dist/ferry.env ] || (cd backend && go run ./cmd/ferry config example) > dist/ferry.env
echo "built dist/ferry$EXT ($VERSION)"
