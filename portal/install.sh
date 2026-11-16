#!/usr/bin/env bash
# =====================================================================
# KARTIKA-AGENT — Installer otomatis
# Mengunduh binary pra-kompilasi dari GitHub Releases (LAPIS 2) dan
# memasangnya sebagai perintah 'kartika' di direktori biner sistem.
#
# PATH otomatis dipasang untuk shell apa pun yang terdeteksi:
#   bash  -> ~/.bashrc            (export PATH=...)
#   zsh   -> ~/.zshrc             (export PATH=...)
#   fish  -> ~/.config/fish/config.fish  (set -gx PATH, tanpa export)
#   sh/ksh/dash/lainnya -> ~/.profile    (export PATH=..., POSIX)
# Paksa shell tertentu: KARTIKA_SHELL=fish bash install.sh
#
# Pemakaian:
#   curl -fsSL https://kartika-agent-portal.vercel.app/install.sh | bash
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
REPO="${KARTIKA_REPO:-airsyahexel-cell/kartika-agent}"
INSTALL_DIR="${KARTIKA_INSTALL_DIR:-/usr/local/bin}"

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
echo "[INSTALL] Repo rilis  : https://github.com/${REPO}"
echo "[INSTALL] Halaman     : https://github.com/${REPO}/releases"

# Bila kartika sudah terpasang, beri tahu jalur pembaruan termudah.
if command -v kartika >/dev/null 2>&1; then
  echo "[INSTALL] Terdeteksi  : kartika $(kartika --version 2>/dev/null | head -1)"
  echo "[INSTALL] Pembaruan   : cukup jalankan 'kartika' lalu ketik /update"
fi
echo "[INSTALL] Mengunduh   : ${URL}"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
curl -fSL "$URL" -o "$TMP/kartika-agent" || {
  echo "[INSTALL] Gagal mengunduh binary dari ${URL}" >&2
  echo "[INSTALL] Release belum tersedia? Gunakan installer dari repo klon:" >&2
  echo "[INSTALL]   git clone <repo> && cd kartika-agent && bash install.sh" >&2
  echo "[INSTALL] (installer repo memakai dist/ lokal atau build via Go)" >&2
  exit 1
}
chmod +x "$TMP/kartika-agent"

if [ -w "$INSTALL_DIR" ]; then
  DEST="$INSTALL_DIR"
else
  DEST="${HOME}/.local/bin"
  mkdir -p "$DEST"
  echo "[INSTALL] $INSTALL_DIR tidak dapat ditulis — fallback ke $DEST"
fi

mv "$TMP/kartika-agent" "$DEST/kartika"
chmod +x "$DEST/kartika"
echo "[INSTALL] Terpasang di: $DEST/kartika"

# Pastikan 'kartika' bisa dipanggil dari shell apa pun.
patch_shell_configs "$DEST"

"$DEST/kartika" --version
echo "[INSTALL] Selesai. Buka terminal baru lalu ketik: kartika"
echo "[INSTALL] Repo    : https://github.com/${REPO}"
echo "[INSTALL] Update  : di dalam kartika ketik /update (cek rilis + pasang binary baru)"
