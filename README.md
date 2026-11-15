# KARTIKA-AGENT

```
██╗  ██╗ █████╗ ██████╗ ████████╗██╗ ██╗  ██╗ █████╗
██║ ██╔╝██╔══██╗██╔══██╗╚══██╔══╝██║██║ ██╔╝██╔══██╗
█████╔╝ ███████║██████╔╝   ██║   ██║█████╔╝ ███████║
██╔═██╗ ██╔══██║██╔══██╗   ██║   ██║██╔═██╗ ██╔══██║
██║  ██╗██║  ██║██║  ██╗   ██║   ██║██║  ██╗██║  ██║
╚═╝  ╚═╝╚═╝  ╚═╝╚═╝  ╚═╝   ╚═╝   ╚═╝╚═╝  ╚═╝╚═╝  ╚═╝
```

**Tactical Autonomous Security & Audit CLI**

| | |
|---|---|
| **Kategori** | 01 Offensive Tech / 03 Core Infra (Automated Security & Configuration Auditing) |
| **Identitas Arsitektur** | Tactical Mission-Driven CLI Agent + Model Context Protocol (MCP) Client |
| **Bahasa Pemrograman** | Golang (100% Native Standard Library — tanpa dependensi pihak ketiga) |

---

## 1. Filosofi Desain

**KARTIKA-AGENT** dibangun dengan gaya *Tactical Command & Briefing Engine* yang menyesuaikan budaya kerja satuan siber untuk mengaudit **aplikasi web/API, jaringan & sistem, serta source code** secara otonom — dengan satu tujuan: **setiap temuan dibuktikan lewat PoC yang dapat direproduksi**, bukan sekadar dugaan.

Agen bersifat **otonom penuh**: setelah operator memberikan satu arahan, ia langsung mencari tahu, merancang rencana verifikasi sendiri, dan **menjalankan apa pun yang dibutuhkan** (probe HTTP, uji aktif via shell, telusur kode) tanpa menunggu persetujuan per langkah — dan tidak dibatasi oleh model engine mana pun yang dipasang (ikuti doktrin yang sama). Audit **tidak dibatasi black-box HTTP publik**: agen menelusuri aplikasi secara menyeluruh termasuk area pasca-login (multi-peran, API terautentikasi) untuk mencari IDOR/BOLA, auth bypass, dsb. Bila target terkunci di balik login dan tidak ada kredensial, agen bertanya **satu kali** ("adakah kredensial uji?") lalu melanjutkan otonom; jika tidak ada, ia memakai jalur sah yang tersedia (registrasi mandiri, kredensial default yang relevan, permukaan tanpa login) — tidak berhenti hanya karena login tertutup.

### A. Paradigma Mission-Driven & Verifikasi Berbasis Bukti (Zero-False Positive)

1. **Mode Posko (Interactive Tactical Shell):** operator memberikan direktif misi di terminal, dan agen merespons menggunakan format **SITREP** (*Situation Report*) yang tegas: *Temuan*, *Bukti Validasi*, *Dampak*, dan *Langkah Mitigasi*.
2. **Mode Operasi Senyap / Headless (`--mission`):** dijalankan dalam satu baris perintah tanpa interaksi manual, cocok ditanamkan langsung ke dalam *CI/CD pipeline* untuk mencegah kode rentan naik ke produksi.
3. **Evidence-Based Verification + PoC:** agen tidak berhenti pada asumsi permukaan. Setiap temuan WAJIB disertai **PoC yang dapat direproduksi** — perintah/probe/payload persis + output aktual yang membuktikannya. Indikasi yang belum terbukti dilaporkan `[INFO]` (zero false positive), lalu dicatat sebagai **SITREP** beserta *patch* perbaikan yang terukur (`write_file`).

### B. Universal Model Provider (Sovereign & Agnostic AI)

Sistem memakai standar **OpenAI-Compatible Chat Completions Schema**. Operator cukup mengganti tiga variabel lingkungan untuk beralih antar-*otak* AI:

| Mode | Provider | `KARTIKA_API_URL` |
|---|---|---|
| Offline / Air-Gapped (markas/satuan) | **Ollama** | `http://localhost:11434/v1` (default) |
| Offline / Air-Gapped (markas/satuan) | **vLLM** | `http://server-satuan:8000/v1` |
| Cloud Cepat (pengujian/evaluasi) | **Groq** | `https://api.groq.com/openai/v1` |
| Cloud Cepat (pengujian/evaluasi) | **Gemini** (OpenAI-compat) | `https://generativelanguage.googleapis.com/v1beta/openai` |
| Cloud Cepat (pengujian/evaluasi) | **OpenRouter** | `https://openrouter.ai/api/v1` |
| Cloud Cepat (pengujian/evaluasi) | **OpenAI** | `https://api.openai.com/v1` |
| Cloud Cepat (pengujian/evaluasi) | **NVIDIA NIM** | `https://integrate.api.nvidia.com/v1` (kunci `nvapi-...`) |

> Dalam lingkungan pertahanan, ketergantungan pada satu vendor AI komersial merupakan risiko strategis — engine lokal menjamin **kedaulatan data** (tidak ada prompt yang keluar dari jaringan internal).

### C. Ekstensibilitas Modular via MCP (Model Context Protocol)

KARTIKA-AGENT mengimplementasikan **MCP Client (JSON-RPC 2.0 over Stdio)** secara *native* di Golang:

1. Saat dijalankan, agen membaca `mcp.json`;
2. menyalakan *MCP Server* eksternal sebagai *child process*;
3. melakukan *handshake* (`initialize` → `notifications/initialized`);
4. menyerap seluruh daftar *tools* (`tools/list`) sebagai modul taktis tambahan;
5. mengeksekusinya lewat `tools/call` saat engine memintanya.

Kemampuan agen dapat diperluas kapan saja (Git forensics, SQLite inspection, Docker audit, network diagnostics) **tanpa mengubah kode inti Golang**.

---

## 2. Alur Pembentukan Sistem

```text
[Tahap 1: Inisialisasi Core Engine]
       │  • Setup modul Go & konfigurasi environment (API_URL, API_KEY, MODEL)
       ▼
[Tahap 2: Pembangunan Built-in Tactical Modules]
       │  • Implementasi scan_dir, read_file, grep_code, http_probe, http_request, dan write_file
       ▼
[Tahap 3: Integrasi MCP Stdio Bridge]
       │  • Parser mcp.json -> Spawn Subprocess -> JSON-RPC Handshake -> Tool Discovery
       ▼
[Tahap 4: Orkestrasi ReAct Loop & Verification Engine]
       │  • Recon -> Inspect & Verify Evidence -> SITREP + Remediation Patch
       ▼
[Tahap 5: Cross-Compilation & Distribusi]
          • Build Binary (Linux/Windows/macOS) -> GitHub Releases -> Vercel Portal
```

### Rincian Alur Eksekusi Internal (Runtime Flow)

1. **Bootstrapping:** program memuat konfigurasi LLM (env/`.env`), membaca `mcp.json`, dan menyatukan modul bawaan serta modul MCP ke dalam satu registri JSON Schema.
2. **Input Direktif Misi:** operator memasukkan perintah melalui terminal interaktif maupun *flag* `--mission`.
3. **Siklus Penalaran & Validasi (ReAct + Verification Loop):**
   - **Fase 1 (Recon):** agen memetakan struktur direktori (`scan_dir`), mengambil profil awal target (`http_probe`), meng-inventarisasi field input & endpoint dari source code (`grep_code`), dan mem-fingerprint versi arsitektur (X-Powered-By, Server, banner layanan).
   - **Fase 2 (Inspect & Verify):** agen membaca *source code* (`read_file`), **melacak rantai field input → sink** (`grep_code` + `read_file`) lalu **membangun payload dari sink aktual** (bukan payload generik), menganalisa **DOM/klien** (innerHTML, location.hash, postMessage, DOM clobbering — dibuktikan via browser MCP), menerapkan **inteligensi CVE** (fingerprint versi → kelemahan/CVE yang diketahui → uji PoC), atau membedah *header*, atribut *cookie*, dan elemen form HTML untuk memverifikasi apakah kelemahan benar-benar valid (menyingkirkan *false positive*).
   - **Fase 3 (Report & Remediate):** agen merangkum hasil validasi ke laporan **SITREP** (dengan bagian **COVERAGE**: aset × vektor × status TERBUKTI/BERSIH/TERBLOKIR/BELUM) dan menulis file *patch*/rekomendasi konfigurasi (`write_file`). Fase ini hanya dimasuki setelah semua aset × vektor relevan berstatus jelas — doktrin **KEDALAMAN & CAKUPAN** melarang berhenti dini atau menyimpulkan dari temuan ringan, dan **WAF/challenge bukan alasan berhenti** (5 jalur bypass wajib dicoba).

Selain vektor klasik, doktrin engine juga mewajibkan:
- **Perburuan sekret & aset non-klasik** — API key/kredensial/kunci yang hardcode di file (source, config, JS, backup, `.env`) ditemukan **dan divalidasi** (dites ke layanannya: login DB, cek AWS STS, forge session framework) — sekret yang valid adalah temuan berat, bukan catatan INFO.
- **Eskalasi dampak & rantai bug (chaining)** — temuan low/info dipikirkan jalur eskalasinya (apa yang terbuka setelah temuan ini?): XSS→ATO, IDOR→akun admin, SSRF→metadata cloud→kredensial, LFI→RCE, sekret di JS→API internal, dll. Temuan yang membuka pintu baru (akses, kredensial, endpoint internal) membuka kembali audit ke area yang baru terjangkau. Di SITREP, rantai dilaporkan sebagai **satu temuan ber-dampak akhir**, bukan beberapa temuan terpisah.

---

## 3. Gaya Interaksi Terminal (Tactical HUD)

```text
[KARTIKA-AGENT // TACTICAL CORE v1.1]
STATUS : ONLINE | ENGINE: llama-3.3-70b | MODUL AKTIF: 10 Built-in + 2 MCP

╭────────────────────────────────────────────────────────────────────╮
│ MISI ❯ Audit postur keamanan pada http://testaspnet.vulnweb.com/   │
╰────────────────────────────────────────────────────────────────────╯

─ KARTIKA ───────────────────────────────────────────────────────────
  ⠹ engine berpikir (2s)
[FASE 1: RECON]   Memetakan struktur target & mengambil profil awal ...
  [MODUL] http_probe({"url":"http://testaspnet.vulnweb.com/login.aspx"})
  [SELESAI] Status 200 OK (HTTP/1.1) | 9 header | 1 cookie | checklist: 6 header keamanan tidak ada
[FASE 2: VERIFY]  Memvalidasi response headers, atribut cookie, dan struktur form HTML ...
[FASE 3: REPORT]  Menyusun SITREP dan rekomendasi hardening konfigurasi ...
  [MODUL] write_file({"path":"sitrep/sitrep-login-aspx.md","content":"# SITREP..."})

SITREP // LAPORAN HASIL AUDIT & VERIFIKASI KEAMANAN
[KRITIS]  1. Transmisi Kredensial Tanpa Enkripsi (Cleartext HTTP)
          - Bukti  : Target merespons langsung di HTTP (Port 80)
            tanpa header 'Strict-Transport-Security' maupun pengalihan HTTPS.
          - PoC    : kartika http_probe {"url":"http://target.local/login.aspx"}
            → form action="http://..." + input type=password tanpa redirect 301
[WASPADA] 2. Kebocoran Sidik Jari Teknologi (Server Fingerprinting)
          - Bukti  : Header 'Server' dan 'X-AspNet-Version'
            mengekspos versi spesifik IIS dan framework ASP.NET secara publik.
          - PoC    : kartika http_probe {"url":"http://target.local/"} → baris Header Respons
[WASPADA] 3. Ketiadaan Proteksi Anti-Clickjacking & MIME Sniffing
          - Bukti  : Tidak ditemukan header 'X-Frame-Options',
            'Content-Security-Policy', maupun 'X-Content-Type-Options'.
          - PoC    : kartika http_probe {"url":"http://target.local/"} → Checklist Security Headers

REKOMENDASI MITIGASI:
- Aktifkan HTTPS dengan HSTS, sembunyikan banner versi pada 'web.config',
  dan terapkan security headers standar pada reverse proxy / IIS.

[MISI SELESAI] langkah penalaran: 4 | eksekusi modul: 3 | laporan tersimpan: sitrep/sitrep-login-aspx.md
```

> Jawaban SITREP mengalir **real-time token demi token** (streaming). Konteks percakapan diingat antar misi selama sesi — lanjutkan dengan pertanyaan seperti `ringkas temuan nomor 2 jadi patch web.config`.

---

## 4. Instalasi

### Opsi A — Installer Lokal (binary dist/, cukup ketik `kartika`)

Cara tercepat bila Anda mempunyai folder proyek ini (atau hasil `build.sh`):

```bash
bash install-user.sh                # pasang ke ~/.local/bin — tanpa root
sudo bash install-user.sh           # atau: pasang ke /usr/local/bin (semua user)
```

Skrip akan: memilih binary `dist/kartika-agent-<os>-<arch>` (build sendiri bila kosong), memasangnya sebagai **`kartika`**, memastikan direktori tujuan ada di `PATH` untuk shell apa pun, dan menyiapkan konfigurasi global di `~/.config/kartika/.env`.

**Dukungan shell (deteksi otomatis):**

| Shell | File config yang dipatch | Sintaks yang ditulis |
|---|---|---|
| bash | `~/.bashrc` | `export PATH="$DEST:$PATH"` |
| zsh | `~/.zshrc` | `export PATH="$DEST:$PATH"` |
| fish | `~/.config/fish/config.fish` | `set -gx PATH "$DEST" $PATH` (dengan guard `contains`) |
| sh/ksh/dash/lainnya | `~/.profile` | `export PATH="$DEST:$PATH"` (POSIX) |

Installer menentukan shell utama dari variabel `$SHELL` (override: `KARTIKA_SHELL=fish bash install-user.sh`), lalu ikut mempatch config shell lain yang filenya sudah ada — cocok bila Anda memakai lebih dari satu shell. Proses ini **idempoten**: dijalankan ulang tidak menduplikasi baris.

Setelah itu buka terminal baru (atau `source` config shell Anda) dan cukup ketik:

```bash
kartika                             # mode posko interaktif
kartika --mission "Audit http://target"   # mode senyap
```

Cara mengaktifkan PATH tanpa buka terminal baru, per shell:

```bash
source ~/.bashrc                    # bash
source ~/.zshrc                     # zsh
source ~/.config/fish/config.fish   # fish (atau tutup-buka terminal)
. ~/.profile                        # sh/ksh/dash
```

### Opsi B — Installer Otomatis (dari Vercel Portal)

```bash
curl -fsSL https://kartika-agent.vercel.app/install.sh | bash
```

Skrip mendeteksi OS/arsitektur dan memasangnya sebagai perintah `kartika` di `/usr/local/bin` (fallback `~/.local/bin` + penyesuaian PATH otomatis untuk bash/zsh/fish/sh). Ketika dijalankan dari dalam repo, installer memakai **urutan sumber** berikut tanpa perlu jaringan:

1. Binary lokal `dist/` (hasil `bash build.sh`).
2. Build langsung dari source Go (jika Go terpasang).
3. Unduh dari GitHub Releases (hanya bila keduanya tidak tersedia — set `KARTIKA_REPO=org/repo`).

### Opsi C — Unduh Manual dari GitHub Releases

```bash
# Linux
curl -fSL -o kartika \
  https://github.com/your-org/kartika-agent/releases/latest/download/kartika-agent-linux-amd64
chmod +x kartika && sudo mv kartika /usr/local/bin/
```

> Sesuaikan `your-org/kartika-agent` dengan lokasi repo organisasi Anda. Opsi ini hanya berlaku setelah organisasi mempublikasikan release.

### Opsi D — Build dari Source

Membutuhkan **Go ≥ 1.21**. Karena proyek multi-package, target build adalah *package root* (`.`):

```bash
git clone https://github.com/your-org/kartika-agent.git
cd kartika-agent
bash build.sh                       # cross-compile 3 platform ke dist/
# atau langsung:
go build -ldflags="-s -w" -o kartika .   # atau: make build
sudo cp kartika /usr/local/bin/     # agar cukup ketik 'kartika'
```

> Alternatif alias tanpa memindah file (pilih sesuai shell Anda):
> - bash/zsh: `echo "alias kartika='$PWD/kartika'" >> ~/.bashrc` (atau `~/.zshrc`)
> - fish: `echo "alias kartika '$PWD/kartika'" >> ~/.config/fish/config.fish`
> - sh: `echo "alias kartika='$PWD/kartika'" >> ~/.profile`

---

## 5. Konfigurasi

Salin `.env.example` menjadi `.env`, atau ekspor variabel lingkungan langsung:

```bash
# Mode Offline / Air-Gapped (Ollama lokal — default)
export KARTIKA_API_URL=http://localhost:11434/v1
export KARTIKA_API_KEY=ollama
export KARTIKA_MODEL=llama3.3

# Mode Cloud Cepat (contoh: Groq)
export KARTIKA_API_URL=https://api.groq.com/openai/v1
export KARTIKA_API_KEY=gsk_xxxxxxxxxxxx
export KARTIKA_MODEL=llama-3.3-70b-versatile
```

### Variabel Lingkungan

| Variabel | Default | Fungsi |
|---|---|---|
| `KARTIKA_API_URL` | `http://localhost:11434/v1` | Base URL endpoint OpenAI-Compatible |
| `KARTIKA_API_KEY` | `ollama` | Bearer token (diabaikan oleh server lokal) |
| `KARTIKA_MODEL` | `llama3.3` | Nama model engine |
| `KARTIKA_MAX_STEPS` | `160` | Batas langkah penalaran ReAct per penugasan |
| `KARTIKA_TEMPERATURE` | `0.2` | Suhu generasi (rendah = deterministik untuk audit) |
| `KARTIKA_TIMEOUT` | `180` | Timeout koneksi ke engine (detik) |
| `KARTIKA_MCP_CONFIG` | `mcp.json` | Path file konfigurasi MCP |
| `KARTIKA_WRITE_SCOPE` | `.` | Cakupan direktori untuk modul `write_file` |

### Urutan Prioritas Konfigurasi

| Prioritas | Sumber | Kapan dipakai |
|---|---|---|
| 1 | Variabel lingkungan asli (`export KARTIKA_*`) | CI/CD, override sesi |
| 2 | `./.env` (direktori kerja saat ini) | Override khusus per proyek/misi |
| 3 | `~/.config/kartika/.env` | **Konfigurasi global** — agar `kartika` bisa dipanggil dari folder mana pun tanpa export |
| 4 | Nilai default bawaan | Tanpa konfigurasi apa pun |

Dengan demikian: pasang binary sekali (`install-user.sh`), isi `~/.config/kartika/.env` satu kali, lalu `kartika` siap dipanggil dari direktori mana pun. File `mcp.json` tetap dibaca dari direktori kerja (per proyek) atau via `--mcp-config <path>`.

---

## 6. Mode Penggunaan

### Mode Posko — Chat Taktis Interaktif (gaya Claude Code / opencode)

```bash
kartika
```

Pengalaman obrolan penuh di terminal: **kotak input berbingkai rounded**, jawaban yang **mengalir real-time token demi token** (streaming SSE OpenAI-Compatible), **indikator kerja berputar** saat engine berpikir, dan **memori percakapan** — konteks misi sebelumnya diingat sehingga Anda bisa menanyakan lanjutan ("ringkas temuan tadi", "ubah mitigasi nomor 2 menjadi patch web.config").

**Perintah slash:**

| Perintah | Fungsi |
|---|---|
| `/status` | Info engine, kunci API, konfigurasi, modul & MCP aktif, jumlah chat |
| `/modul` | Daftar seluruh modul taktis (built-in + MCP) |
| `/mcp` | Status MCP server & lokasi konfigurasi mcp.json |
| `/pikir` | Tampilkan/sembunyikan alur berpikir model (reasoning) |
| `ctrl+t` / `alt+1..9` | Tab sesi baru / pindah tab (layar penuh) |
| `kartika -s` / `--lanjut [id]` | Daftar sesi / buka ulang sesi tersimpan |

## 7. Pustaka Skill (metodologi bug bounty & red team)

Kartika membawa **pustaka skill** — metodologi profesional per kelas kerentanan (gerbang validasi, tabel payload, bypass, rantai eskalasi) dari repositori [murrtada/bug-bounty-agent-skills](https://github.com/murrtada/bug-bounty-agent-skills), ditempatkan di `~/.local/share/kartika/skills/`. Tiga modul khusus tersedia bagi engine:

| Modul | Fungsi |
|---|---|
| `skill_list` | daftar seluruh skill + deskripsi singkat |
| `skill_read` | baca metodologi penuh satu skill (mis. `hunt-ssrf`, `hunt-jwt-crypto`) |
| `skill_search` | cari payload/kriteria bukti lintas pustaka (mis. `"blind ssrf oob"`) |

Doktrin engine: panggil `skill_list` di awal penugasan; sebelum menguji kelas kerentanan tertentu baca skill-nya dan **ikuti gerbang validasinya** (zero false positive). Lokasi pustaka dapat diarahkan lewat `KARTIKA_SKILLS_DIR`. `install.sh` meng-clone pustaka otomatis; unduh manual:

```bash
git clone https://github.com/murrtada/bug-bounty-agent-skills.git \
  ~/.local/share/kartika/skills/bug-bounty-agent-skills
```
| `/bersih` | Bersihkan layar dan tampilkan ulang banner |
| `/baru` | Sesi baru — hapus memori percakapan |
| `/bantuan` | Tampilkan bantuan |
| `/keluar` | Tutup sesi posko |
| `<teks>` | Chat / pertanyaan — jawaban mengalir real-time |

> Perintah tanpa garis miring (`status`, `modul`, `bantuan`, `keluar`) tetap dikenali demi kompatibilitas. **Mengetik `/` di kotak input langsung memunculkan menu saran perintah** (gaya Claude Code): panah ▲▼ memilih, `Tab`/`→` melengkapi, `Enter` mengirim. Jika engine tidak terhubung (API/model belum dikonfigurasi), kartika menampilkan panduan letak `KARTIKA_API_URL` / `KARTIKA_API_KEY` / `KARTIKA_MODEL` di `~/.config/kartika/.env` alih-alih error jaringan mentah. Pada terminal non-interaktif (piped/CI), kotak input, menu saran, dan spinner otomatis diganti output polos. Jika engine tidak mendukung `stream: true`, jawaban otomatis fallback ke mode non-streaming tanpa kehilangan fungsi.

**Pemikiran model (reasoning)** — alur berpikir engine (delta `reasoning_content`, mis. NVIDIA NIM) disembunyikan di latar belakang secara default. Selagi engine berpikir, di bawah indikator spinner tampil petunjuk `ctrl+b memunculkan pemikiran model di latar belakang ke depan` (digambar segera saat mulai). Tekan **`ctrl+b`** kapan saja (saat engine bekerja maupun di kotak input) untuk menampilkan/menyembunyikan blok `── BERPIKIR ──`, atau gunakan `/pikir`. Narasi antar-langkah ("let me check…", "I will test…") **tidak mengotori layar** — hanya garis modul yang berjalan dan jawaban/SITREP final yang tampil; narasi hanya terlihat lewat blok pemikiran.

**Layar penuh + sesi tersimpan (mode posko di terminal)** — pada terminal interaktif kartika membuka **layar penuh miliknya sendiri** (alternate screen, gaya aplikasi terminal opencode): transkrip chat mengikuti ekor, bar status berisi id sesi + petunjuk `ctrl+b`, input + menu slash di bawah. Keluar (`/keluar`, `Ctrl+C`, EOF) otomatis **menyimpan sesi** ke `~/.local/share/kartika/sessions/sesi-<id>.json` (disimpan ulang juga setiap giliran selesai). Buka ulang sesi terakhir beserta seluruh memori percakapannya:

```bash
kartika --lanjut
```

Di layar penuh, transkrip bisa di-scroll kembali ke atas: **PgUp/PgDn** (per halaman) atau **↑/↓** (satu baris, saat menu slash tertutup) — bar status menampilkan indikator `▲ N baris`; menekan Enter (kirim) kembali mengikuti ekor. Ukuran terminal (termasuk maximize) mengikuti otomatis, dan saat engine berpikir bar status beranimasi.

**Pesan yang Anda ketik di-echo** ke transkrip (`❯ <pesan>`) sehingga jejak percakapan utuh terbaca saat scroll ke atas.

**Tab multi-sesi (ala opencode)** — `ctrl+t` membuat tab sesi baru (engine & memori percakapan independen), `alt+1..9` berpindah tab; strip tab tampil di header dengan tombol `[+tab]`. Tiap tab menyimpan sesinya sendiri.

**Manajemen sesi:**

```bash
kartika -s            # daftar sesi tersimpan (id, giliran, waktu, cuplikan)
kartika --lanjut      # buka ulang sesi terbaru
kartika --lanjut <id> # buka ulang sesi tertentu
```

### Mode Senyap / Headless (CI-CD)

```bash
kartika --mission "Audit postur keamanan pada http://target/login.aspx, verifikasi setiap kelemahan, dan susun SITREP mitigasi" --no-color
```

Contoh integrasi **GitHub Actions** (quality gate audit):

```yaml
- name: KARTIKA-AGENT Audit Gate
  env:
    KARTIKA_API_URL: ${{ secrets.KARTIKA_API_URL }}
    KARTIKA_API_KEY: ${{ secrets.KARTIKA_API_KEY }}
    KARTIKA_MODEL:   ${{ secrets.KARTIKA_MODEL }}
  run: |
    curl -fSL -o kartika https://github.com/your-org/kartika-agent/releases/latest/download/kartika-agent-linux-amd64
    chmod +x kartika
    ./kartika --mission "Audit source code pada ./src dan susun SITREP" --no-color
```

### Flag CLI

| Flag | Fungsi |
|---|---|
| `--mission "teks"` | Eksekusi misi tunggal (headless) lalu keluar |
| `--provider <nama>` | Profil provider: `ollama` `vllm` `groq` `openai` `openrouter` `gemini` |
| `--mcp-config <path>` | Path konfigurasi MCP (default `mcp.json`) |
| `--no-mcp` | Lewati pemuatan MCP server |
| `--no-color` | Matikan warna ANSI (log CI rapi) |
| `--max-steps <n>` | Batas langkah penalaran |
| `--version` | Tampilkan versi |

---

## 7. Modul Taktis Built-in (10 Modul)

| Modul | Fase | Fungsi |
|---|---|---|
| `scan_dir` | RECON | Memetakan struktur direktori: folder, file, ukuran (skip `.git`, `node_modules`, dsb.) |
| `read_file` | VERIFY | Membaca source code/konfigurasi **dengan nomor baris** agar temuan dapat dikutip sebagai bukti |
| `grep_code` | VERIFY | Pencarian pola di source code (substring/regex, filter glob `*.php`/`*.js`, nomor baris): melacak **field input → sink** (`$_GET`, `req.query`, `query(`, `echo`, `exec`, `include`, `innerHTML`, `location.hash`, `postMessage`) — dasar analisa source→payload dan analisa DOM/klien |
| `http_probe` | RECON/VERIFY | Probe HTTP/HTTPS: status, rantai redirect, seluruh header, atribut cookie (`HttpOnly`/`Secure`/`SameSite`), banner teknologi, **checklist security headers** (HSTS, CSP, X-Frame-Options, X-Content-Type-Options, Referrer-Policy, Permissions-Policy), analisis body (form, action cleartext, input password), **+ enumerasi aset & tautan** (script `.js`, stylesheet, iframe, link internal) sebagai bahan telusur menyeluruh |
| `http_request` | VERIFY | **Request HTTP arbitrer dengan sesi cookie persisten antar langkah** (`session="user"`/`"admin"` — login sekali, semua panggilan berikutnya membawa sesi) + **mode `diff`**: membandingkan respons dua request/dua identitas pada endpoint sama dalam satu panggilan — perbedaan body = bukti **broken access control/IDOR**. Metode/headers/body apa pun, cookie jar bertahan antar langkah & antar sesi kartika (`cookies.json`), `action=list_sessions` untuk inspeksi jar |
| `run_cmd` | VERIFY | **Uji aktif via shell di mesin operator**: recon jaringan (`nmap`, `dig`, `ss`, `ps`), uji endpoint/TLS (`curl`, `openssl`), triase kode (`grep`, `find`) — dengan timeout (default 120s, maks 600s) dan output dipotong 64 KB. Satu perintah per pemanggilan. HANYA untuk target yang diizinkan |
| `write_file` | REPORT | Menulis laporan SITREP Markdown + bukti PoC / file patch konfigurasi (dibatasi di dalam cakupan `KARTIKA_WRITE_SCOPE`) |
| `skill_list` | METODOLOGI | Daftar seluruh skill pustaka metodologi (dinamis dari folder pustaka) |
| `skill_read` | METODOLOGI | Membaca metodologi satu skill lengkap (gerbang validasi + tabel payload) |
| `skill_search` | METODOLOGI | Mencari payload/kriteria pembuktian lintas seluruh skill pustaka |

Output seluruh modul berupa teks polos yang langsung dikonsumsi engine sebagai **bukti verifikasi**.

---

## 8. Ekspansi via MCP Server

Salin `mcp.json.example` menjadi `mcp.json`, lalu sesuaikan:

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "."]
    },
    "git": {
      "command": "uvx",
      "args": ["mcp-server-git", "--repository", "."]
    },
    "sqlite": {
      "command": "uvx",
      "args": ["mcp-server-sqlite", "--db-path", "./app.db"]
    },
    "playwright": {
      "command": "npx",
      "args": ["-y", "@playwright/mcp@latest", "--browser", "chromium", "--headless"]
    }
  }
}
```

**Playwright MCP (browser Chromium)** — sudah aktif di `mcp.json` repo ini. Agen bisa membuka halaman, klik form, mengisi input, dan mengambil snapshot DOM/headers sebagai bukti audit web interaktif (login flow, XSS DOM, dsb.). Prasyarat sekali-jalan:

```bash
npx -y @playwright/mcp@latest --version   # unduh paket MCP
npx -y playwright@1.57.0 install chromium # unduh browser Chromium
```

> Config bawaan sekarang **menampilkan jendela Chromium di depan** saat agen bekerja (mode headed). Untuk server/CI tanpa layar, tambahkan kembali argumen `"--headless"`.

- Seluruh tool MCP tampil dengan nama `mcp_<server>_<tool>` dan deskripsi berprefiks `[MCP:<server>]`.
- Server yang gagal dimuat (executable tidak ada, handshake timeout) **tidak menghentikan** agen — hanya dilaporkan `[MCP] <nama> GAGAL` saat boot.
- Gunakan `--no-mcp` untuk menjalankan agen murni dengan 5 modul built-in.

---

## 9. Build Multi-Platform & Distribusi

```bash
# Linux (Server / CachyOS / Ubuntu)
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o dist/kartika-agent-linux-amd64 .

# Windows
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o dist/kartika-agent-windows-amd64.exe .

# macOS (Apple Silicon)
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o dist/kartika-agent-darwin-arm64 .

# Semua sekaligus
bash build.sh        # atau: make dist
```

> **Catatan:** proyek ini multi-package (`internal/*`), sehingga target build adalah `.` (package root), bukan `main.go` tunggal.

Setiap *push tag* `v*` memicu workflow `.github/workflows/release.yml` yang membangun ketiga binary dan mempublikasikannya ke **GitHub Releases** (LAPIS 2).

---

## 10. Arsitektur Deployment

```text
+-------------------------------------------------------------------+
|  LAPIS 1: VERCEL (Distribution & Documentation Landing Portal)    |
|  • URL Publik: https://kartika-agent.vercel.app                   |
|  • Menyediakan dokumentasi arsitektur & skrip /install.sh         |
+---------------------------------+---------------------------------+
                                  | Mengunduh Binary Otomatis
                                  ▼
+-------------------------------------------------------------------+
|  LAPIS 2: GITHUB RELEASES (Multi-OS Compiled Binaries)            |
|  • kartika-agent-linux-amd64                                      |
|  • kartika-agent-windows-amd64.exe                                |
|  • kartika-agent-darwin-arm64                                     |
+---------------------------------+---------------------------------+
                                  | Dijalankan di Terminal Operator
                                  ▼
+-------------------------------------------------------------------+
|  LAPIS 3: LINGKUNGAN EKSEKUSI LOKAL (Workstation / CI-CD)         |
|  • Mode Posko (Interaktif) atau Mode Headless (--mission)         |
|  • Terhubung ke Ollama (Offline) atau Cloud API + MCP Servers     |
+-------------------------------------------------------------------+
```

### Deploy Portal ke Vercel

Folder `portal/` berisi landing statis + `install.sh` + `vercel.json`:

```bash
cd portal && vercel deploy --prod
# atau: import repo di dashboard Vercel, set Root Directory = portal
```

---

## 11. Struktur Proyek

```text
kartika-agent/
├── main.go                        # Entry point: flag CLI, boot, mode posko & headless, MCP loader
├── go.mod                         # Module tanpa dependensi eksternal
├── internal/
│   ├── config/config.go           # .env loader + KARTIKA_* env + preset provider
│   ├── ui/ui.go                   # Tactical HUD: logo, banner, fase, bingkai SITREP, warna ANSI
│   ├── llm/client.go              # Klien OpenAI-Compatible (chat completions + tools, retry)
│   ├── tools/
│   │   ├── registry.go            # Registri modul seragam (built-in + MCP) -> JSON Schema
│   │   └── builtin.go             # scan_dir, read_file, grep_code, http_probe, http_request, write_file
│   ├── mcp/client.go              # MCP Client: JSON-RPC 2.0 over stdio (spawn, handshake, discovery, call)
│   └── agent/loop.go              # ReAct Loop + Verification Engine + doktrin system prompt
├── mcp.json.example               # Templat konfigurasi MCP Server
├── .env.example                   # Templat konfigurasi engine AI
├── build.sh                       # Cross-compile 3 platform -> dist/
├── Makefile
├── install.sh                     # Installer otomatis dari GitHub Releases
├── install-user.sh                # Installer lokal dari dist/ (cukup ketik 'kartika')
├── .github/workflows/release.yml  # CI: build & publikasi release pada tag v*
└── portal/                        # LAPIS 1: landing Vercel (index.html, vercel.json, install.sh)
```

---

## 12. Keamanan & Etika Penggunaan

- KARTIKA-AGENT dirancang untuk **audit defensif** terhadap sistem milik sendiri atau sistem yang telah memperoleh **izin tertulis**. Penggunaan terhadap target tanpa izin melanggar hukum dan disiplin satuan.
- Modul `write_file` dibatasi pada cakupan direktori kerja (`KARTIKA_WRITE_SCOPE`) untuk mencegah penulisan di luar area audit.
- Kredensial engine disimpan pada variabel lingkungan/`.env` — jangan meng-commit `.env` (sudah dikecualikan di `.gitignore`).
- Laporan misi tersimpan di `sitrep/` (dikecualikan dari kontrol versi).
