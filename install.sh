#!/usr/bin/env bash
# =====================================================================
# KARTIKA-AGENT — Installer otomatis
# Memasang binary 'kartika' ke direktori biner sistem. Urutan sumber:
#   1. binary lokal  dist/          (jika menjalankan dari repo)
#   2. build dari source Go          (fallback jika go tersedia)
#   3. unduh GitHub Releases         (LAPIS 2 distribusi, jika ada)
#
# PATH otomatis dipasang untuk shell apa pun yang terdeteksi:
#   bash  -> ~/.bashrc            (export PATH=...)
#   zsh   -> ~/.zshrc             (export PATH=...)
#   fish  -> ~/.config/fish/config.fish  (set -gx PATH, tanpa export)
#   sh/ksh/dash/lainnya -> ~/.profile    (export PATH=..., POSIX)
# Paksa shell tertentu: KARTIKA_SHELL=fish bash install.sh
#
# Pemakaian:
#   bash install.sh                      # dari repo (pakai dist/ atau build)
#   curl -fsSL https://kartika-agent.vercel.app/install.sh | bash
#   # atau dengan repo kustom:
#   KARTIKA_REPO=satuan-siber/kartika-agent bash install.sh
#
# Setelah terpasang, jalankan cukup dengan mengetik:
#   kartika
# =====================================================================
set -euo pipefail

# ---------------------------------------------------------------------
# patch_shell_configs <dir>
# Menambahkan <dir> ke PATH pada config shell yang relevan. Idempoten:
# jalankan berkali-kali tidak menduplikasi baris (penanda "KARTIKA-AGENT PATH").
# Config shell login utama selalu dipatch (dibuat bila belum ada); config
# shell lain ikut dipatch bila filenya sudah ada di sistem.
# ---------------------------------------------------------------------
patch_shell_configs() {
  dir="$1"
  marker="# KARTIKA-AGENT PATH"

  # a) Tentukan config shell login utama (env SHELL, atau override KARTIKA_SHELL).
  shell_name="$(basename "${KARTIKA_SHELL:-${SHELL:-}}")"
  case "$shell_name" in
    bash) main_shell="$HOME/.bashrc" ;;
    zsh)  main_shell="$HOME/.zshrc" ;;
    fish) main_shell="$HOME/.config/fish/config.fish" ;;
    *)    main_shell="$HOME/.profile" ;;   # sh/ksh/dash/tidak dikenal
  esac

  # b) Kumpulkan config shell lain yang sudah ada (dukungan multi-shell).
  set -- "$main_shell"
  [ -f "$HOME/.bashrc" ]                      && set -- "$@" "$HOME/.bashrc"
  [ -f "$HOME/.zshrc" ]                       && set -- "$@" "$HOME/.zshrc"
  [ -f "$HOME/.config/fish/config.fish" ]     && set -- "$@" "$HOME/.config/fish/config.fish"
  [ -f "$HOME/.profile" ]                     && set -- "$@" "$HOME/.profile"

  patched=""
  for f in "$@"; do
    mkdir -p "$(dirname "$f")"
    [ -f "$f" ] || touch "$f"
    if grep -q "$marker" "$f" 2>/dev/null; then
      continue  # sudah dipatch sebelumnya — jangan duplikasi
    fi
    case "$f" in
      *config.fish)
        # fish tidak mengenal 'export' — gunakan set -gx + guard contains
        # (kompatibel fish 2.x/3.x/4.x, tanpa duplikasi tiap startup).
        printf '\n%s\nif not contains "%s" $PATH\n    set -gx PATH "%s" $PATH\nend\n' \
          "$marker" "$dir" "$dir" >> "$f"
        ;;
      *)
        # bash/zsh/POSIX: sintaks export standar.
        printf '\n%s\nexport PATH="%s:$PATH"\n' "$marker" "$dir" >> "$f"
        ;;
    esac
    patched="$patched $(basename "$f")"
  done

  if [ -n "$patched" ]; then
    echo "[INSTALL] PATH '$dir' ditambahkan ke:$patched"
    case ":$PATH:" in
      *":$dir:"*) ;;
      *) echo "[INSTALL] Aktifkan sekarang: 'source <config shell Anda>' atau buka terminal baru" ;;
    esac
  else
    echo "[INSTALL] PATH '$dir' sudah terdaftar di config shell (tidak diubah)"
  fi
}

# Sesuaikan dengan lokasi repo GitHub organisasi Anda (LAPIS 2 distribusi).
# Hanya dipakai bila binary lokal tidak tersedia dan 'go' tidak terpasang.
REPO="${KARTIKA_REPO:-your-org/kartika-agent}"
INSTALL_DIR="${KARTIKA_INSTALL_DIR:-/usr/local/bin}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"

OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
  Linux) os="linux" ;;
  Darwin) os="darwin" ;;
  CYGWIN*|MINGW*|MSYS*) os="windows" ;;
  *) echo "[INSTALL] OS tidak didukung: $OS" >&2; exit 1 ;;
esac

case "$ARCH" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) echo "[INSTALL] Arsitektur tidak didukung: $ARCH" >&2; exit 1 ;;
esac

ASSET="kartika-agent-${os}-${arch}"
[ "$os" = "windows" ] && ASSET="${ASSET}.exe"
URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"

echo "[INSTALL] Mendeteksi  : ${os}-${arch}"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# ---------------------------------------------------------------------
# Urutan sumber binary: (1) dist/ lokal, (2) build dari source, (3) unduh.
# ---------------------------------------------------------------------
SRC="$TMP/kartika-agent"

if [ -f "$SCRIPT_DIR/dist/$ASSET" ]; then
  echo "[INSTALL] Sumber      : dist lokal ($SCRIPT_DIR/dist/$ASSET)"
  cp "$SCRIPT_DIR/dist/$ASSET" "$SRC"
elif [ -f "$SCRIPT_DIR/main.go" ] && command -v go >/dev/null 2>&1; then
  echo "[INSTALL] Sumber      : build dari source (go build)"
  ( cd "$SCRIPT_DIR" && go build -ldflags="-s -w" -o "$SRC" . )
else
  echo "[INSTALL] Sumber      : unduh dari GitHub Releases"
  echo "[INSTALL] Mengunduh   : ${URL}"
  curl -fSL "$URL" -o "$SRC" || {
    echo "[INSTALL] Gagal mengunduh binary." >&2
    echo "[INSTALL] Solusi: jalankan 'bash build.sh' di repo, lalu ulangi install.sh," >&2
    echo "[INSTALL] atau pasang Go agar installer bisa build dari source." >&2
    exit 1
  }
fi

chmod +x "$SRC"

if [ -w "$INSTALL_DIR" ]; then
  DEST="$INSTALL_DIR"
else
  DEST="${HOME}/.local/bin"
  mkdir -p "$DEST"
  echo "[INSTALL] $INSTALL_DIR tidak dapat ditulis — fallback ke $DEST"
fi

mv "$SRC" "$DEST/kartika"
chmod +x "$DEST/kartika"
echo "[INSTALL] Terpasang di: $DEST/kartika"

# Pustaka SKILL metodologi (bug bounty & red team) — clone bila belum ada.
SKILL_DIR="${HOME}/.local/share/kartika/skills"
if [ ! -d "$SKILL_DIR/bug-bounty-agent-skills" ]; then
  if command -v git >/dev/null 2>&1; then
    mkdir -p "$SKILL_DIR"
    echo "[SKILL] Mengunduh pustaka skill metodologi (murrtada/bug-bounty-agent-skills)…"
    if git clone --depth 1 https://github.com/murrtada/bug-bounty-agent-skills.git "$SKILL_DIR/bug-bounty-agent-skills" >/dev/null 2>&1; then
      N=$(find "$SKILL_DIR" -name SKILL.md | wc -l)
      echo "[SKILL] $N skill termuat @ $SKILL_DIR"
    else
      echo "[SKILL] Clone gagal (offline?) — unduh manual: git clone https://github.com/murrtada/bug-bounty-agent-skills.git \"$SKILL_DIR/bug-bounty-agent-skills\""
    fi
  else
    echo "[SKILL] git tidak tersedia — unduh manual pustaka skill ke $SKILL_DIR"
  fi
fi

# Pastikan 'kartika' bisa dipanggil dari shell apa pun.
patch_shell_configs "$DEST"

"$DEST/kartika" --version
echo "[INSTALL] Selesai. Buka terminal baru lalu ketik: kartika"
