#!/usr/bin/env bash
export CGO_ENABLED=0 GOAMD64=v1
# =====================================================================
# KARTIKA-AGENT — Build & Cross-Compilation Multi-Platform
# Catatan: proyek ini multi-package (internal/*), sehingga target build
# adalah package root ('.'), bukan file main.go tunggal.
#
# Versi ditanam ke binary lewat -X main.version. Sumber versi, berurutan:
#   1. $KARTIKA_VERSION (mis. KARTIKA_VERSION=1.2.0 bash build.sh)
#   2. git describe --tags (tag vX.Y.Z terdekat)
#   3. nilai default di main.go (tanpa -X)
# Tujuannya agar '/update' dan 'kartika --version' melaporkan versi yang
# benar pada binary hasil build lokal, bukan versi bawaan sumber.
# =====================================================================
set -euo pipefail
cd "$(dirname "$0")"

export GOOS GOARCH
mkdir -p dist

VER="${KARTIKA_VERSION:-}"
if [ -z "$VER" ] && command -v git >/dev/null 2>&1; then
  VER="$(git describe --tags --abbrev=0 2>/dev/null || true)"
fi
VER="${VER#v}"

LDFLAGS="-s -w"
if [ -n "$VER" ]; then
  LDFLAGS="$LDFLAGS -X main.version=$VER"
  echo "[BUILD] Versi tertanam: v$VER"
else
  echo "[BUILD] Versi: default dari main.go (set KARTIKA_VERSION atau buat git tag untuk menanam versi)"
fi

build() {
  local out="$1" os="$2" arch="$3"
  echo "[BUILD] $out"
  GOOS="$os" GOARCH="$arch" go build -ldflags="$LDFLAGS" -o "dist/$out" .
}

build kartika-agent-linux-amd64  linux   amd64
build kartika-agent-windows-amd64.exe windows amd64
build kartika-agent-darwin-arm64 darwin  arm64

echo "[SELESAI] Binary tersedia pada folder dist/"
command -v ls >/dev/null && du -h dist/* 2>/dev/null || true

# Checksum lokal (SHA-256) — dicetak agar bisa dibandingkan dengan
# digest aset pada halaman rilis GitHub.
if command -v sha256sum >/dev/null 2>&1; then
  ( cd dist && sha256sum kartika-agent-* 2>/dev/null || true )
elif command -v shasum >/dev/null 2>&1; then
  ( cd dist && shasum -a 256 kartika-agent-* 2>/dev/null || true )
fi
