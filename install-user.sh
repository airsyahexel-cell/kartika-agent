#!/usr/bin/env bash
# =====================================================================
# KARTIKA-AGENT — Installer Lokal (Linux/macOS)
# Memasang binary dari dist/ (atau build dari source bila dist kosong)
# sebagai perintah 'kartika' agar bisa dipanggil cukup dengan mengetik:
#
#   kartika
#
# PATH otomatis dipasang untuk shell apa pun yang terdeteksi:
#   bash  -> ~/.bashrc            (export PATH=...)
#   zsh   -> ~/.zshrc             (export PATH=...)
#   fish  -> ~/.config/fish/config.fish  (set -gx PATH, tanpa export)
#   sh/ksh/dash/lainnya -> ~/.profile    (export PATH=..., POSIX)
# Paksa shell tertentu: KARTIKA_SHELL=fish bash install-user.sh
#
# Pemakaian (dari root folder proyek):
#   bash install-user.sh            # instal ke ~/.local/bin (tanpa root)
#   sudo bash install-user.sh       # instal ke /usr/local/bin (seluruh user)
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

# ---------------------------------------------------------------------
# 1. Tentukan sumber binary: dist/ sesuai OS-ARCH, atau build sendiri.
# ---------------------------------------------------------------------
OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
  Linux)  os="linux" ;;
  Darwin) os="darwin" ;;
  *) echo "[INSTALL] OS tidak didukung: $OS (Windows: pakai dist/*.exe manual)" >&2; exit 1 ;;
esac

case "$ARCH" in
  x86_64|amd64)   arch="amd64" ;;
  aarch64|arm64)  arch="arm64" ;;
  *) echo "[INSTALL] Arsitektur tidak didukung: $ARCH" >&2; exit 1 ;;
esac

BIN="dist/kartika-agent-${os}-${arch}"

if [ ! -f "$BIN" ]; then
  echo "[INSTALL] $BIN tidak ditemukan — mencoba build dari source..."
  if ! command -v go >/dev/null 2>&1; then
    echo "[INSTALL] Go tidak ditemukan. Pasang Go >= 1.21 lalu jalankan:" >&2
    echo "          go build -ldflags=\"-s -w\" -o \"$BIN\" ." >&2
    exit 1
  fi
  mkdir -p dist
  go build -ldflags="-s -w" -o "$BIN" .
  echo "[INSTALL] Build selesai: $BIN"
fi

# ---------------------------------------------------------------------
# 2. Pilih direktori tujuan yang dapat ditulis.
# ---------------------------------------------------------------------
if [ "$(id -u)" -eq 0 ]; then
  DEST="/usr/local/bin"
else
  DEST="${HOME}/.local/bin"
fi
mkdir -p "$DEST"

# ---------------------------------------------------------------------
# 3. Pasang binary sebagai 'kartika'.
# ---------------------------------------------------------------------
cp "$BIN" "$DEST/kartika"
chmod +x "$DEST/kartika"
echo "[INSTALL] Terpasang di: $DEST/kartika"

# Pastikan 'kartika' bisa dipanggil dari shell apa pun.
patch_shell_configs "$DEST"

# ---------------------------------------------------------------------
# 4. Siapkan konfigurasi global agar 'kartika' jalan dari folder mana pun.
#    Prioritas: env asli > ./.env (folder aktif) > file global ini.
# ---------------------------------------------------------------------
CFG_DIR="${HOME}/.config/kartika"
mkdir -p "$CFG_DIR"
if [ ! -f "$CFG_DIR/.env" ]; then
  if [ -f ".env" ]; then
    cp ".env" "$CFG_DIR/.env"
    echo "[INSTALL] Konfigurasi global disalin: $CFG_DIR/.env"
  elif [ -f ".env.example" ]; then
    cp ".env.example" "$CFG_DIR/.env"
    echo "[INSTALL] Contoh konfigurasi disalin ke $CFG_DIR/.env"
    echo "[INSTALL] >> EDIT file itu: isi KARTIKA_API_URL / KARTIKA_API_KEY / KARTIKA_MODEL"
  fi
else
  echo "[INSTALL] Konfigurasi global sudah ada: $CFG_DIR/.env (dipertahankan)"
fi

# ---------------------------------------------------------------------
# 5. Verifikasi.
# ---------------------------------------------------------------------
"$DEST/kartika" --version
echo "[INSTALL] Selesai. Sekarang cukup ketik: kartika"
