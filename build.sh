#!/usr/bin/env bash
# Build LAN Dash: frontend (Vite) -> embedded into a single static Go binary.
#
#   ./build.sh                 # builds bin/lan-dash for the host architecture
#   GOARCH=arm64 ./build.sh    # cross-compile (e.g. for a Raspberry Pi 4/5)
#
# Requirements: Node.js >= 18 with npm, Go >= 1.22.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERSION="${VERSION:-$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo 0.3.0)}"
OUT="${OUT:-$ROOT/bin/lan-dash}"

need() { command -v "$1" >/dev/null 2>&1 || { echo "error: '$1' not found in PATH ($2)" >&2; exit 1; }; }
need npm "install Node.js 18+ (e.g. via https://github.com/nodesource/distributions)"
need go "install Go 1.22+ from https://go.dev/dl/"

echo "==> Building frontend"
cd "$ROOT/frontend"
if [[ -f package-lock.json ]]; then npm ci --no-audit --no-fund; else npm install --no-audit --no-fund; fi
npm run build

echo "==> Embedding frontend into backend/dist"
find "$ROOT/backend/dist" -mindepth 1 ! -name .placeholder -exec rm -rf {} +
cp -R "$ROOT/frontend/dist/." "$ROOT/backend/dist/"

echo "==> Building Go binary (CGO disabled, static) version=$VERSION"
cd "$ROOT/backend"
mkdir -p "$(dirname "$OUT")"
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$OUT" ./cmd/landash

echo "==> Done: $OUT ($(du -h "$OUT" | cut -f1))"
echo "    Install with: sudo ./install.sh"
