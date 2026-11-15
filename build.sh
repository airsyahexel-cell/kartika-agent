#!/usr/bin/env bash
export CGO_ENABLED=0 GOAMD64=v1
# =====================================================================
# KARTIKA-AGENT — Build & Cross-Compilation Multi-Platform
# Catatan: proyek ini multi-package (internal/*), sehingga target build
# adalah package root ('.'), bukan file main.go tunggal.
# =====================================================================
set -euo pipefail
cd "$(dirname "$0")"

export GOOS GOARCH
mkdir -p dist

echo "[BUILD] kartika-agent-linux-amd64"
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o dist/kartika-agent-linux-amd64 .

echo "[BUILD] kartika-agent-windows-amd64.exe"
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o dist/kartika-agent-windows-amd64.exe .

echo "[BUILD] kartika-agent-darwin-arm64"
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o dist/kartika-agent-darwin-arm64 .

echo "[SELESAI] Binary tersedia pada folder dist/"
command -v ls >/dev/null && du -h dist/* 2>/dev/null || true
