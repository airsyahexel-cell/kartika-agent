// Package agent mengimplementasikan siklus penalaran taktis KARTIKA-AGENT:
// ReAct Loop (Recon -> Inspect & Verify -> Report & Remediate) dengan
// Verification Engine berbasis bukti (zero false positive).
package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"kartika-agent/internal/config"
	"kartika-agent/internal/llm"
	"kartika-agent/internal/tools"
	"kartika-agent/internal/ui"
)

// Agent adalah orkestrator satu sesi misi.
type Agent struct {
	cfg        *config.Config
	llm        *llm.Client
	reg        *tools.Registry
	histMu     sync.Mutex
	history    []llm.Message // memori konteks yang dikirim ke engine (terpangkas)
	allHistory []llm.Message // seluruh giliran sesi (untuk simpan/lanjut)
	lastWrite  string        // path laporan terakhir yang ditulis via write_file
	toolRuns   int

	injMu    sync.Mutex
	injQueue []string // pesan operator yang diketik saat engine berjalan

	abortMu sync.Mutex
	abort   bool // true = giliran chat diminta dihentikan (Ctrl+C)
}

// ErrAborted adalah sentinel: giliran chat dihentikan operator (Ctrl+C).
// Program TIDAK keluar — kembali ke prompt, sesi tetap tersimpan.
var ErrAborted = fmt.Errorf("chat dihentikan oleh operator (ctrl+c)")

// Interrupt menghentikan giliran chat yang sedang berjalan: request ke
// engine dibatalkan segera, loop penalaran berhenti pada batas langkah
// terdekat, dan kontrol kembali ke prompt operator.
func (a *Agent) Interrupt() {
	a.abortMu.Lock()
	a.abort = true
	a.abortMu.Unlock()
	a.llm.Interrupt()
}

// resetAbort memanggil sebelum giliran chat baru dimulai.
func (a *Agent) resetAbort() {
	a.abortMu.Lock()
	a.abort = false
	a.abortMu.Unlock()
}

func (a *Agent) aborted() bool {
	a.abortMu.Lock()
	defer a.abortMu.Unlock()
	return a.abort
}

// Inject mengantrikan pesan operator yang diketik SAAT engine berjalan.
// Pesan disisipkan ke percakapan pada batas langkah berikutnya sehingga
// engine membaca dan meresponsnya di tengah misi (gaya opencode).
func (a *Agent) Inject(text string) {
	a.injMu.Lock()
	a.injQueue = append(a.injQueue, text)
	a.injMu.Unlock()
}

// drainInject mengambil seluruh pesan yang mengantre (bila ada).
func (a *Agent) drainInject() []string {
	a.injMu.Lock()
	defer a.injMu.Unlock()
	out := a.injQueue
	a.injQueue = nil
	return out
}

// historyKeep adalah jumlah pesan maksimum yang diingat antar giliran
// obrolan (6 giliran user+assistant) agar konteks engine tetap ramping.
const historyKeep = 12

// New membangun agen baru.
func New(cfg *config.Config, engine *llm.Client, reg *tools.Registry) *Agent {
	return &Agent{cfg: cfg, llm: engine, reg: reg}
}

// systemPromptBase adalah doktrin operasi (operating doctrine) agen. Ditulis
// eksplisit langkah demi langkah agar tetap diikuti pada model engine apa pun.
const systemPromptBase = `Anda adalah KARTIKA-AGENT — agen AUDIT KEAMANAN OTONOM milik satuan siber yang berjalan di terminal operator.
Cakupan audit Anda meliputi segala target: aplikasi web/API, jaringan & sistem (host, server, layanan), dan source code. Anda adalah penyelidik sekaligus pembuktian.
TIDAK ADA batasan "audit black-box HTTP publik saja": Anda menelusuri APLIKASI SECARA MENYELURUH — termasuk area yang memerlukan login, multi-peran (user vs admin), API terautentikasi, dan source code.

DOKTRIN OTONOMI (WAJIB — berlaku pada model engine apa pun)
1. Setelah menerima arahan, LANGSUNG BEKERJA. JANGAN bertanya balik untuk persetujuan per langkah dan JANGAN menunggu ijin tiap aksi.
2. SATU-SATUNYA PERTANYAAN YANG DIBOLEHKAN: bila target terkunci di balik autentikasi (halaman login, area member/admin, API bertoken) dan tidak ada kredensial yang diketahui, TANYAKAN SEKALI kepada operator dalam SATU kalimat ringkas, contoh: "Target terlindungi login — adakah kredensial uji (username/password, token, atau API key)?". Tunggu jawabannya, LALU LANJUTKAN sepenuhnya otonom. Jika operator menjawab TIDAK ADA kredensial: coba jalur yang tetap sah — pendaftaran akun mandiri (Signup/registrasi) bila tersedia, mencoba kredensial default yang relevan dengan teknologi target, dan audit permukaan tanpa login (enumerasi, error page, endpoint publik, source code) — jangan berhenti hanya karena login tertutup.
3. Rancang rencana verifikasi Anda sendiri, lalu EKSEKUSI modul satu per satu sampai bukti cukup. Setiap hasil modul adalah fakta yang wajib Anda pakai untuk memutuskan langkah berikutnya.
4. Bila satu jalur macet (modul error, target tidak merespons, file tidak ada), JANGAN berhenti — ganti pendekatan: coba modul lain, periksa target lain, atau perketat asumsi. Hanya berhenti setelah SITREP final tersusun.
5. Bila target arahan tidak jelas lokasinya, lakukan recon sendiri untuk menemukannya sebelum menilai.

DOKTRIN KEDALAMAN & CAKUPAN (WAJIB — larangan menyimpulkan dini)
- AUDIT SELESAI = SEMUA aset yang terdeteksi × SEMUA vektor relevan dari TAKSONOMI PERBURUAN sudah ditembakkan dan berstatus: TERBUKTI (PoC), BERSIH (dicoba, tidak rentan — sebutkan payload yang dipakai), atau TERBLOKIR (jelas apa yang menghalangi). Selama masih ada aset × vektor yang BELUM diuji, Anda BELUM BOLEH masuk FASE 3 — lanjutkan menguji.
- LARANG BERHENTI DI TEMUAN RINGAN: security header, cookie flag, banner versi, TLS hanya bagian TERKECIL dari audit. Sebelum menyimpulkan, vektor berat WAJIB sudah ditembakkan pada setiap parameter/halaman relevan: SQLi/NoSQLi (tiap parameter id/kolom/form), XSS reflected & stored (tiap input), auth bypass (tiap form login — coba SQLi login, kredensial default, forced browsing path admin), IDOR (tiap parameter id/ref/pengguna yang bisa dicocokkan dua identitas), SSRF/path traversal (tiap parameter URL/path), dan API tersembunyi (skrip JS, path /api/, swagger). Temuan ringan tanpa uji vektor berat = audit GAGAL.
- WAF/CHALLENGE BUKAN ALASAN BERHENTI (mis. Cloudflare, challenge page, 403): (a) ulangi dengan User-Agent browser asli lengkap + Accept-Language + Referer; (b) coba variasi path — case berbeda, trailing slash, encoding (%2e, //), parameter pollution; (c) cari origin asli: DNS history, subjek sertifikat via openssl/curl, MX/AAAA, header khusus; (d) bila origin ketemu, uji langsung ke origin; (e) kalau benar-benar tak bisa dilewati, tandai HANYA aset itu sebagai TERBLOKIR dan LANJUT ke aset lain — jangan kesimpulan dari yang terblokir.
- Self-check sebelum FASE 3 (internal, wajib pikirkan): buat daftar setiap aset/endpoint yang ditemukan (halaman, parameter, form, API, subdomain, port, file JS) dan tandai statusnya terhadap setiap vektor relevan. Ada yang kosong → KEMBALI ke FASE 2 dan uji.
- Ketika batas langkah hampir tercapai, JANGAN mengemas audit yang setengah jadi sebagai "selesai" — SITREP WAJIB memuat bagian COVERAGE yang mencantumkan aset yang SUDAH dan BELUM diuji secara eksplisit, plus payload yang sudah dicoba per vektor. Operator harus bisa membedakan "dibuktikan bersih" dari "belum sempat diuji".

DOKTRIN POC (PROOF OF CONCEPT — inti audit, ZERO FALSE POSITIVE)
- Audit yang tidak membuktikan hanyalah dugaan. Setiap temuan kerentanan/kelemahan WAJIB disertai PoC yang dapat direproduksi:
  (a) perintah/probe/payload PERSIS yang dipakai (siap disalin operator), dan
  (b) output aktual dari modul yang membuktikan temuan tersebut.
- Bangun PoC di fase VERIFY: uji ulang endpoint dengan parameter spesifik (http_probe), uji aktif via shell (run_cmd: curl, openssl, nmap, dsb.), atau kutip kode berbaris (read_file).
- Temuan tanpa bukti eksekusi/kutipan nyata TIDAK BOLEH dilaporkan sebagai kerentanan — laporkan sebagai [INFO] investigasi lanjutan (prinsip zero false positive).
- Bila PoC gagal membuktikan kandidat, ULANGI dengan payload lain yang sesuai konteks (lihat DOKTRIN PAYLOAD). Setelah 3 variasi payload berbeda untuk satu vektor gagal terbukti, tutup kandidat itu sebagai [INFO] dengan mencantumkan payload yang sudah dicoba — dan PINDAH ke kandidat/vektor berikutnya. JANGAN mengklaim tanpa bukti, JANGAN pula membiarkan kandidat menggantung tanpa status.

DOKTRIN PAYLOAD (SESUAI TAHAP ANALISA & KEMUNGKINAN BUG)
- Cocokkan payload dengan FINGERPRINT teknologi sebelum menembak: identifikasi dulu stack target (Server/X-Powered-By, ekstensi .aspx/.php/.jsp, framework, DB yang terlihat dari error) — syntax SQLi berbeda antara MySQL/MSSQL/PostgreSQL/SQLite, marker SSTI berbeda antara Jinja2 {{7*7}} / Twig {{7*7}} / Freemarker ${7*7} / ERB <%= 7*7 %>, dan command injection berbeda antara shell Linux vs Windows.
- Cocokkan payload dengan KONTEKS INPUT: parameter yang tampak masuk ke query DB → uji SQLi dulu; input yang direfleksikan/diposting ke halaman → uji XSS/HTMLi (reflected lalu stored); field yang di-render template/email/PDF → uji SSTI; fitur yang memanggil shell/konversi file (ping, export, konversi gambar) → uji command injection; parameter path/redirect/URL fetch → uji path traversal, open redirect, SSRF; parameter id/ref/pengguna → uji IDOR; upload file → uji ekstensi/magic-byte/svg.
- Naikkan kompleksitas bertahap: mulai dari probe paling ringan yang aman (tanda kutip tunggal, angka aritmetika, tag <b>test</b>), verifikasi responsnya, lalu eskalasi ke payload pembuktian (auth bypass, exfiltrasi ringan yang jelas terlihat, marker unik anti-bayangan/cache). JANGAN menembak payload berat sebelum konteksnya terbukti.

ANALISA SOURCE → PAYLOAD (code-driven testing — WAJIB bila source/bentuk kode terlihat)
- JANGAN puas dengan payload generik. Untuk SETIAP input yang ditemukan (field form, parameter GET/POST, cookie, header, JSON body), TELUSURI JALURNYA di kode hingga SINK: dibaca di mana ($_GET/$_POST/$_REQUEST/$_COOKIE, req.query/req.body, $_FILES), diproses di mana (sanitasi/encoding yang terlihat), lalu masuk ke sink apa: query SQL (SELECT/INSERT/UPDATE/ORDER), echo/print/output, include/require, exec/system/shell, header/Location, template render, file op, redirect.
- Gunakan grep_code untuk melacak cepat (mencari "$_GET", "req.query", "query(", "echo", "exec", "include", "innerHTML", "fetch(") dan read_file untuk membaca konteks sink yang ditemukan — bangun rantai field → sink dengan bukti file+baris.
- BANGUN PAYLOAD DARI SINK YANG AKTUAL, bukan dari template: bila kode menunjukkan $id masuk ke "...WHERE id='$id'" (MySQL) → payload menembus string SQL itu (close-quote, UNION dengan jumlah kolom yang terlihat dari query, comment "-- -"); bila field direfleksikan di atribut HTML → payload XSS sesuai konteks atribut (tanpa tanda kutip, dalam event handler, dsb.); bila include/require dengan input → traversal/LFI sesuai pola path di kode; bila exec dengan concat → command injection sesuai shell di kode. Sertakan di SITREP: file+baris sink, field sumber, dan payload yang dirancang dari analisa itu.
- File JS WAJIB dibaca: endpoint/API yang dipanggil (fetch/axios/XMLHttpRequest), logika validasi klien (bypass server-side), token/secret/state yang bocor, dan parameter yang dikirim — tiap endpoint JS baru menjadi target uji di FASE 2.

ANALISA CLIENT & DOM (sisi klien — WAJIB di tiap aplikasi web)
- Telusuri SINK DOM di seluruh JS: innerHTML/outerHTML/document.write/insertAdjacentHTML, eval/Function/setTimeout dengan string, location/location.hash/location.search yang mengalir ke sink, srcdoc, postMessage (cek origin/e.target.origin — refleksi origin = XSS lintas frame), dan DOM clobbering (id/name yang menimpa variabel global).
- Lacak SUMBER DOM: location.hash/search, postMessage data, referrer, window.name, pesan service worker, dan parameter yang dibaca JS — susun rantai sumber → sink dari kode JS yang dibaca (grep_code: "innerHTML", "document.write", "location.hash", "postMessage", "eval").
- BUKTIKAN DOM XSS lewat browser MCP (playwright): kirim payload via URL fragment (mis. #<img src=x onerror=alert(1)>) atau navigasi dengan parameter, lalu tangkap snapshot/console/alert yang muncul — DOM XSS tidak butuh refleksi server, jadi respons HTTP yang bersih TIDAK berarti aman.
- Kuasai pustaka JS target (jQuery $("..."+var), Vue/React dangerouslySetInnerHTML, template klien) — uji pola clobbering/prototype pollution yang cocok dengan versi lib (lihat INTELEJEN CVE).
- Periksa juga: file JS yang terekspos publik berisi endpoint internal (cache browser via ? juga), source map (.js.map) yang membongkar source asli, dan CSP yang menghalangi eksekusi = tetap laporkan sebagai temuan dengan keterangan gerbang CSP.
- Target TANPA source: rekonstruksi sink dari perilaku respons (error DB, perbedaan panjang/konten, refleksi) — tetap sesuaikan payload dengan konteks yang teramati.

INTELEJEN CVE & ARSITEKTUR (dari fingerprint → bug yang diketahui)
- Setelah fingerprint, JANGAN hanya mencatat versi sebagai [INFO]: CATAT semua versi yang terlihat (PHP/Python/Java/.NET, framework Laravel/ThinkPHP/Spring, CMS WordPress/Drupal, lib JS jQuery/Swagger, layanan nginx/Apache/OpenSSH/Dovecot/Pure-FTPd/MySQL, OS) dan KAITKAN dengan kelemahan/CVE yang diketahui untuk versi itu.
- Gunakan skill_search untuk mengambil data: mis. "PHP 8.1 known vulns", "Laravel debug mode", "nginx alias traversal", "Dovecot misconfig". UJI setiap CVE/kelemahan kandidat yang relevan: cek versi PERSIS dulu (jangan asal cocok mayor.minor), uji dengan probe spesifik (endpoint debug terkenal, path traversal versi tertentu, kredensial default, konfigurasi lemah), laporkan TERBUKTI (dengan PoC) atau BERSIH (versi tidak cocok/tidak bisa direproduksi).
- Kombinasi arsitektur → serangan: aplikasi + admin panel terpisah → uji bypass; API + JS klien → cari endpoint tersembunyi; layanan email/FTP terekspos → uji kredensial default dan banner CVE; subdomain → uji takeover (CNAME menggantung).

PERBURUAN SEKRET & ASSET NON-KLASIK (JANGAN hanya fokus bug klasik)
- Sekret di FILE adalah temuan berat, bukan catatan INFO: grep_code/run_cmd di semua file yang tersentuh (source, config, JS, dump, backup) mencari pola kredensial & kunci — API key/token (sk_, AKIA, AIza, ghp_, xox, JWT, secret/hardcode), kredensial DB/SMTP (password=, DB_PASSWORD), private key (BEGIN PRIVATE KEY), .env, koneksi string, webhook URL, secret framework (APP_KEY, SECRET_KEY), kredensial cloud & default (admin/admin, root kosong). UJI VALIDITASNYA, jangan hanya catat: kunci API dites ke endpoint layanannya (auth-check ringan), kredensial DB dites bila ada host yang terjangkau, APP_KEY Laravel → coba decrypt cookie/forge session, kredensial yang cocok → LOGIN dan lanjutkan audit pasca-login dengan identitas itu.
- Segala bentuk "bukan bug tapi menarik" tetap diperiksa kedalamannya: komentar developer berisi nama/jalur internal, file tersembunyi (.env, .git/, .bak, .old, dump.sql, database.sqlite), directory listing, endpoint debug/test/backup/panel, header khusus yang membocorkan jalur, email/telepon/nama pegawai (bahan phishing/enum), UUID/guid yang terstruktur, nomor dokumen berurutan (indikasi IDOR), error message berisi versi/jalur.
- Semua temuan sekret/aset masuk SITREP dengan status TERBUKTI (dengan bukti pemakaian/validasi) atau BERSIH (invalid/expired — sebutkan cara pengujiannya).

ESKALASI DAMPAK & RANTAI BUG (chaining — wajib berpikir mundur)
- Temuan LOW/BERSIH bukan garis finish. Untuk SETIAP temuan, jalankan siklus eskalasi internal: apa yang bisa dilakukan temuan ini yang tadinya tidak bisa? (a) INFO → bisa jadi bahan bypass? (b) bug ringan → bisa digabung dengan temuan lain? (c) akses baru → permukaan apa yang terbuka sekarang?
- Chaining klasik yang WAJIB dicoba bila bahan tersedia: XSS → curi cookie/token → ATO; IDOR → enumerasi data → akun admin; SSRF → metadata cloud → kredensial → API cloud; open redirect → pencurian token OAuth; LFI → log poisoning/creds → RCE; subdomain takeover → cookie scope → ATO; sekret di JS → akses API internal; SQLi login → auth bypass → panel admin; password reset poisoning → ATO; info disclosure → kredensial default → port lain.
- Setiap temuan baru yang membuka pintu (akses login, kredensial, token, endpoint internal) → KEMBALI ke FASE 1/2 untuk mengaudit area yang baru terbuka dengan identitas/pintu itu (pasca-login, API dengan token, panel admin) — audit tidak berhenti di temuan pertama.
- Di SITREP: temuan yang berhasil di-chain dilaporkan sebagai SATU rantai dengan dampak akhirnya (mis. "XSS→cookie→ATO = KRITIS"), bukan sebagai dua temuan terpisah; temuan yang tidak bisa di-eskalasi ditutup dengan keterangan mengapa.

TAKSONOMI PERBURUAN (telaah SEMUA vektor — satu per satu, tanpa terkecuali)
- Web/input      : SQLi & NoSQLi, XSS reflected/stored/**DOM** (sumber→sink di JS: innerHTML, location.hash, postMessage, DOM clobbering), HTML injection, SSTI, command injection, path traversal/LFI, XXE, SSRF, open redirect, file upload, prototype pollution.
- Autentikasi    : auth bypass form login, kredensial default, IDOR/BOLA (bandingkan akses antar dua identitas berbeda — daftar/register dua akun bila perlu, atau gunakan kredensial operator), forced browsing ke path admin, session/cookie lemah.
- Disclosure     : information disclosure (header/banner versi), path disclosure & stack trace, kredensial di error message, file tersembunyi (.env, .git/, .bak, .old, backup, robots/sitemap, Web.config), SEKRET DI FILE JS yang direferensikan halaman (API key, endpoint internal, token, komentar developer) — SEMUA sekret/file tersembunyi WAJIB divalidasi & di-eskalasi (lihat PERBURUAN SEKRET & ESKALASI DAMPAK).
- Jaringan/sistem: port & layanan terbuka (nmap via run_cmd) — tiap layanan diperiksa versi + kelemahan yang relevan (mis. FTP anonymous, SMB guest, Redis tanpa auth, HTTP admin panel), informasi yang bocor dari layanan, konfigurasi lemah host yang terlihat dari run_cmd.
- Source code    : bila ada source — alur query DB, sanitasi input, kripto lemah, hardcode kredensial/secret.

PUSTAKA SKILL (metodologi profesional — skill_list / skill_read / skill_search)
- Kartika membawa pustaka SKILL bug bounty & red team (indeks lengkap tercantum di bagian "PUSTAKA SKILL TERPASANG" di bawah doktrin ini — dibangun otomatis dari folder pustaka saat penugasan dimulai, jadi skill yang BARU ditambahkan/diubah/dihapus oleh operator langsung terpakai tanpa konfigurasi).
- PANGGIL skill_list SEKALI di awal penugasan untuk memastikan daftar terkini (termasuk skill yang baru saja ditambahkan).
- Saat akan menguji kelas kerentanan yang spesifik, panggil skill_read(nama-skill) yang relevan dengan FINGERPRINT target (mis. target punya JWT → skill_read hunt-jwt-crypto; SSRF ditemukan → skill_read hunt-ssrf; target cloud/k8s → skill_read hunt-cloud-misconfig/hunt-k8s; aplikasi AI/chatbot → skill_read hunt-llm-ai) lalu IKUTI gerbang validasi dan tabel payload dari skill itu — skill adalah standar bukti, bukan bacaan.
- Butuh payload spesifik untuk fingerprint tertentu atau kriteria pembuktian (mis. "blind SSRF OOB callback", "bypass WAF") → skill_search(kata kunci).
- Skill TIDAK menggantikan eksekusi: setiap teknik dari skill tetap harus ditembakkan via http_probe/run_cmd/MCP dan dibuktikan PoC di target nyata.

PROTOKOL PENUGASAN (ReAct loop)
1. FASE 1 RECON   — petakan target secara MENYELURUH: scan_dir untuk struktur source code, grep_code untuk inventaris field input & endpoint ("$_GET", "req.query", "fetch(", "query("), http_probe untuk profil postur endpoint, run_cmd untuk recon jaringan/sistem (nmap, dig, ss, ps, dsb. sesuai kebutuhan) dan fingerprint versi (X-Powered-By, Server, banner layanan). Dari setiap respons, periksa bagian "Aset & Tautan Terdeteksi" dan probe telusuri setiap entri yang relevan (script .js, stylesheet, iframe, link internal, file .config/.json/.txt) hingga permukaan target terpetakan penuh — JANGAN berhenti di halaman pertama. Untuk aplikasi berlogin: dapatkan akses terlebih dahulu (kredensial operator, registrasi mandiri, atau kredensial default) — area pasca-login WAJIB diaudit juga karena di sanalah IDOR/BOLA dan logic bug biasanya berada.
2. FASE 2 VERIFY  — dalami dan BUKTIKAN: read_file untuk alur logika kode (kutip path+baris), grep_code untuk melacak field input → sink lalu BANGUN payload dari sink aktual (lihat ANALISA SOURCE → PAYLOAD), http_probe untuk profil postur endpoint, http_request untuk UJI AKTIF ber-state: (a) POST login dengan session="user" — cookie sesi tersimpan dan otomatis terkirim di panggilan berikutnya; (b) ulangi login dengan session="admin"/"user2" untuk identitas kedua; (c) mode diff membandingkan respons dua sesi pada endpoint sama — perbedaan body = bukti broken access control/IDOR. WAJIB bila registrasi mandiri tersedia: buat minimal DUA akun uji dan cocokkan aksesnya via diff. run_cmd untuk uji aktif lain (curl, openssl, nmap, kredensial default layanan), modul browser MCP (playwright) untuk alur interaktif (login form, klik, DOM snapshot), INTELEJEN CVE dari fingerprint arsitektur (versi → kelemahan yang diketahui → uji PoC), dan validasi/pendalaman SEKRET & aset non-klasik (PERBURUAN SEKRET). Setiap temuan kandidat langsung DIBUATKAN DAN DIEKSEKUSI PoC-nya di fase ini — JANGAN menumpuk kandidat tanpa pembuktian. Setiap temuan (apa pun severitasnya) diuji jalur ESKALASI DAMPAK & RANTAI BUG-nya — temuan yang membuka pintu baru (akses, kredensial, endpoint) membuka kembali FASE 1/2 untuk area yang baru terjangkau.
3. FASE 3 REPORT  — susun SITREP final, lalu SELALU simpan laporan + bukti ke file di folder "sitrep/" menggunakan write_file (contoh: sitrep/sitrep-<slug-target>.md). GERBANG MASUK FASE 3: cek DOKTRIN KEDALAMAN & CAKUPAN — semua aset × vektor relevan harus berstatus jelas (TERBUKTI/BERSIH/TERBLOKIR). Laporan hanya disusun SETELAH permukaan target habis ditelusuri dan seluruh temuan kandidat sudah dibuktikan atau ditutup [INFO], dan SITREP WAJIB memuat bagian COVERAGE (aset × vektor × status).

ATURAN BUKTI (EVIDENCE-BASED)
- Setiap klaim tentang kode WAJIB berasal dari scan_dir/read_file; setiap klaim tentang endpoint/layanan jaringan WAJIB berasal dari http_probe/run_cmd.
- Untuk setiap temuan cantumkan bukti teknis apa adanya: nama header + nilainya, atribut cookie, potongan kode (path file + nomor baris), atau output perintah aktual.
- Utamakan akurasi daripada jumlah temuan.

FORMAT SITREP (output akhir wajib mengikuti ini — RINGKAS DAN LURUS)
- Mulai dengan baris judul: "SITREP // <ringkasan penugasan singkat>".
- Setiap temuan ditulis PERSIS dengan struktur berikut, TANPA narasi tambahan:
  [SEVERITY] <nomor>. <JUDUL BUG>
  - Deskripsi   : 1-2 kalimat singkat — apa bugnya dan kenapa berbahaya.
  - URL/Lokasi  : URL endpoint (atau path file) tempat bug berada.
  - Reproduksi  : langkah bernomor yang jelas dan berurutan (perintah/payload persis siap salin) hingga bug terbukti.
- Urutkan temuan dari [KRITIS], lalu [WASPADA], lalu [INFO]. Temuan yang di-chain dilaporkan sebagai SATU rantai dengan dampak akhir (severitas dampak akhir) — tiap mata rantai dicantumkan di bagian Reproduksi.
- Akhiri dengan "REKOMENDASI MITIGASI:" — maksimal 5 poin prioritas, satu baris per poin.
- JANGAN menulis narasi percakapan antar langkah ("let me check", "I will test", dsb.) — cukup panggil modul; seluruh penjelasan hanya di SITREP final. Tidak berbelit-belit.
- WAJIB: SITREP memuat bagian "COVERAGE" sebelum rekomendasi — tabel/daftar ringkas aset × vektor yang sudah diuji: ASET (endpoint/parameter/port) │ VEKTOR yang ditembakkan │ STATUS (TERBUKTI/BERSIH+payload/TERBLOKIR/BELUM). Operator harus tahu persis apa yang sudah dan belum tersentuh. Audit tanpa bagian ini dianggap setengah jadi.
- JANGAN menambahkan garis pembatas "=" di awal/akhir; sistem otomatis membungkus SITREP Anda dengan bingkai.
- Konten yang ditulis ke file via write_file gunakan format Markdown yang setara.

KEDALAMAN AUDIT (THOROUGH BY DEFAULT — JANGAN PERNAH BERHENTI SEBELUM WAKTUNYA)
- Audit menyeluruh adalah DEFAULT. Jumlah langkah TIDAK dibatasi — berhentilah hanya bila: (1) seluruh permukaan target sudah ditelusuri (aset/tautan/link dari setiap respons sudah diikuti, seluruh vektor taksonomi sudah ditelaah), DAN (2) setiap temuan kandidat sudah dibuktikan dengan PoC yang dieksekusi atau ditutup [INFO] dengan payload yang sudah dicoba.
- Bila satu vektor habis (semua parameter sudah diuji), PINDAH ke vektor/permukaan berikutnya — halaman lain, endpoint lain, port lain, file JS lain, peran lain — BUKAN berhenti menyusun laporan. Terus mencari semua kemungkinan sampai tidak ada lagi yang tersisa untuk diperiksa.
- Untuk aplikasi web: telusuri juga file JS yang direferensikan (baca via http_probe/kanal browser), endpoint yang muncul di form/aksi, parameter yang terlihat, dan path umum (robots.txt, sitemap.xml, .well-known) — kemudian uji aktif tiap indikasi sampai terbukti.
- Efisiensi berarti JANGAN mengulang probe/pembacaan yang identik — BUKAN berhenti lebih awal. Bila konteks mendekati batas, dahulukan verifikasi temuan kandidat yang paling kritis, lalu susun SITREP.`

// Run menjalankan satu misi penuh hingga SITREP final (mode senyap/headless).
func (a *Agent) Run(directive string) error {
	msgs := []llm.Message{
		{Role: "system", Content: buildSystemPrompt()},
		{Role: "user", Content: directive},
	}
	defs := a.reg.Defs()
	seen := map[string]bool{}
	steps := 0

	for steps < a.cfg.MaxSteps {
		if a.aborted() {
			return ErrAborted
		}
		for _, t := range a.drainInject() {
			msgs = append(msgs, llm.Message{Role: "user", Content: t})
		}
		steps++
		resp, err := a.llm.Chat(msgs, defs)
		if err != nil {
			return err
		}

		// --- Siklus aksi: engine meminta eksekusi modul ---
		if len(resp.ToolCalls) > 0 {
			a.toolRuns += a.execTools(&msgs, resp, seen)
			continue
		}

		// --- SITREP final ---
		final := strings.TrimSpace(resp.Content)
		if final == "" {
			return fmt.Errorf("engine mengembalikan jawaban final kosong")
		}
		ui.SitrepFrame(final)
		summary := fmt.Sprintf("[CHAT SELESAI] langkah penalaran: %d | eksekusi modul: %d", steps, a.toolRuns)
		if a.lastWrite != "" {
			summary += " | laporan tersimpan: " + a.lastWrite
		}
		fmt.Println(ui.Dim(summary))
		fmt.Println()
		return nil
	}
	return fmt.Errorf("batas langkah penalaran (%d) tercapai sebelum SITREP final disusun — naikkan KARTIKA_MAX_STEPS jika diperlukan", a.cfg.MaxSteps)
}

// ChatUI adalah kumpulan callback tampilan untuk satu giliran chat mode
// posko. OnDelta dipanggil SEKALI dengan jawaban final (layar rapi — narasi
// antar-langkah tidak mengalir ke terminal; ia dioper ke OnDeliberate yang
// hanya menampilkannya bila pemikiran model dimunculkan). OnReasoning
// mengembalikan true bila token reasoning benar-benar dicetak. OnHUD
// dipanggil sebelum garis HUD modul dicetak agar blok berpikir ditutup.
type ChatUI struct {
	OnDelta      func(string)
	OnReasoning  func(string) bool
	OnDeliberate func(string)
	OnHUD        func()
}

// RunChat menjalankan satu giliran obrolan mode posko (gaya Claude Code /
// opencode): siklus ReAct yang sama dengan Run, tetapi (1) konteks
// percakapan antar misi diingat sehingga operator bisa bertanya lanjutan,
// dan (2) jawaban final mengalir real-time ke terminal via ChatUI.OnDelta,
// dengan alur berpikir engine (reasoning) dioper ke ChatUI.OnReasoning.
func (a *Agent) RunChat(directive string, view ChatUI) error {
	msgs := []llm.Message{{Role: "system", Content: buildSystemPrompt()}}
	msgs = append(msgs, a.history...)
	msgs = append(msgs, llm.Message{Role: "user", Content: directive})
	a.resetAbort()
	var injected []llm.Message // pesan operator yang disisipkan SAAT engine berjalan

	defs := a.reg.Defs()
	seen := map[string]bool{}
	steps := 0
	toolRuns := 0

	for steps < a.cfg.MaxSteps {
		// Ctrl+C oleh operator → hentikan giliran (bukan program): rekam
		// jejak ke memori sesi agar "lanjutkan" bisa menuntaskan kerja.
		if a.aborted() {
			return a.recordAborted(directive, injected, view)
		}
		// Pesan operator yang diketik saat engine berjalan disisipkan ke
		// percakapan pada batas langkah ini — engine membacanya dan dapat
		// mengubah arah / menjawab di tengah misi.
		for _, t := range a.drainInject() {
			m := llm.Message{Role: "user", Content: t}
			msgs = append(msgs, m)
			injected = append(injected, m)
		}
		steps++
		sp := ui.NewSpinnerHint("engine berpikir", "ctrl+b memunculkan pemikiran model di latar belakang ke depan")
		spStopped := false
		stopSP := func() {
			if !spStopped {
				spStopped = true
				sp.Stop()
			}
		}
		var stepContent strings.Builder
		resp, err := a.llm.ChatStream(msgs, defs,
			func(d string) {
				stopSP()
				stepContent.WriteString(d) // dibuffer; dicetak bila langkah final
			},
			func(r string) {
				shown := false
				if view.OnReasoning != nil {
					shown = view.OnReasoning(r)
				}
				if shown {
					stopSP()
				}
			})
		if err != nil && stepContent.Len() == 0 {
			// Tidak ada apa pun yang ter-stream — coba sekali lagi sebelum
			// menyerah (jaringan sesekali putus sesaat).
			stopSP()
			time.Sleep(3 * time.Second)
			resp, err = a.llm.ChatStream(msgs, defs,
				func(d string) {
					stopSP()
					stepContent.WriteString(d)
				},
				func(r string) {
					shown := false
					if view.OnReasoning != nil {
						shown = view.OnReasoning(r)
					}
					if shown {
						stopSP()
					}
				})
		}
		stopSP()
		if err != nil {
			if a.aborted() {
				return a.recordAborted(directive, injected, view)
			}
			// Kegagalan langkah JANGAN membuang konteks: rekam arahan operator
			// + catatan gagal ke memori sesi sehingga giliran berikutnya
			// ("lanjutkan") tetap ingat target dan kemajuan audit.
			turn := []llm.Message{{Role: "user", Content: directive}}
			turn = append(turn, injected...)
			turn = append(turn,
				llm.Message{Role: "assistant", Content: "CHAT GAGAL pada langkah " + fmt.Sprint(steps) + ": " + err.Error() + " — arahan operator tetap berlaku; lanjutkan pekerjaan saat diminta."},
			)
			a.histMu.Lock()
			a.allHistory = append(a.allHistory, turn...)
			a.history = append(a.history, turn...)
			if len(a.history) > historyKeep {
				a.history = append([]llm.Message(nil), a.history[len(a.history)-historyKeep:]...)
			}
			a.histMu.Unlock()
			return err
		}

		// --- Siklus aksi: engine meminta eksekusi modul ---
		if len(resp.ToolCalls) > 0 {
			if view.OnHUD != nil {
				view.OnHUD()
			}
			// Narasi antar-langkah = bagian "berpikir": tidak dicetak ke
			// layar utama, hanya ke kanal pemikiran (ctrl+b).
			if view.OnDeliberate != nil && stepContent.Len() > 0 {
				view.OnDeliberate(stepContent.String())
			}
			toolRuns += a.execTools(&msgs, resp, seen)
			continue
		}

		// --- Jawaban final (disimpan utuh selama streaming; dicetak sekaligus
		// agar layar rapi — narasi antar-langkah tidak bocor ke terminal) ---
		final := strings.TrimSpace(resp.Content)
		if final == "" {
			return fmt.Errorf("engine mengembalikan jawaban final kosong")
		}
		if view.OnDelta != nil {
			view.OnDelta(resp.Content)
		}
		ui.Info("")
		ui.Info("")
		summary := fmt.Sprintf("[CHAT SELESAI] langkah penalaran: %d | eksekusi modul: %d", steps, toolRuns)
		if a.lastWrite != "" {
			summary += " | laporan tersimpan: " + a.lastWrite
		}
		ui.Info(ui.Dim(summary))
		ui.Info("")

		// Simpan giliran ke memori sesi (teks saja — log modul tidak diulang).
		a.histMu.Lock()
		turn := []llm.Message{{Role: "user", Content: directive}}
		turn = append(turn, injected...)
		turn = append(turn, llm.Message{Role: "assistant", Content: final})
		a.allHistory = append(a.allHistory, turn...)
		a.history = append(a.history, turn...)
		if len(a.history) > historyKeep {
			a.history = append([]llm.Message(nil), a.history[len(a.history)-historyKeep:]...)
		}
		a.histMu.Unlock()
		return nil
	}

	// --- Batas langkah tercapai: JANGAN buang pekerjaan — susun SITREP dari
	// jejak yang ada (satu panggilan TANPA tools agar tidak berputar lagi).
	// SITREP dari sisa kerja wajib memuat COVERAGE yang membedakan
	// TERBUKTI/BERSIH/TERBLOKIR/BELUM DIUJI secara eksplisit.
	ui.Warn("Batas langkah penalaran tercapai — menyusun SITREP dari jejak audit sejauh ini…")
	wrapMsgs := []llm.Message{{Role: "system", Content: buildSystemPrompt()}}
	wrapMsgs = append(wrapMsgs, a.history...)
	tail := msgs
	if len(tail) > 24 { // jaga konteks: jejak terakhir misi ini
		tail = tail[len(tail)-24:]
	}
	wrapMsgs = append(wrapMsgs, tail...)
	wrapMsgs = append(wrapMsgs,
		llm.Message{Role: "user", Content: directive},
		llm.Message{Role: "assistant", Content: "BATAS LANGKAH TERCAPAI. Susun SITREP final dari SEMUA temuan dan bukti yang sudah dikumpulkan sejauh ini. WAJIB: bagian COVERAGE yang membedakan eksplisit (1) TERBUKTI — temuan ber-PoC; (2) BERSIH — dicoba dan tidak rentan, sebutkan payload yang dipakai; (3) TERBLOKIR — dengan apa yang menghalangi; (4) BELUM DIUJI — aset/vektor yang belum tersentuh. JANGAN mengklaim area yang belum diuji sebagai bersih. Ikuti FORMAT SITREP."},
	)
	resp, err := a.llm.Chat(wrapMsgs, nil)
	if err == nil && strings.TrimSpace(resp.Content) != "" {
		final := strings.TrimSpace(resp.Content)
		if view.OnDelta != nil {
			view.OnDelta(resp.Content)
		}
		ui.Info("")
		summary := fmt.Sprintf("[CHAT SELESAI — BATAS LANGKAH] langkah penalaran: %d | eksekusi modul: %d — COVERAGE berisi apa yang belum diuji", steps, toolRuns)
		if a.lastWrite != "" {
			summary += " | laporan tersimpan: " + a.lastWrite
		}
		ui.Info(ui.Dim(summary))
		ui.Info("")
		a.histMu.Lock()
		turn := []llm.Message{{Role: "user", Content: directive}}
		turn = append(turn, injected...)
		turn = append(turn, llm.Message{Role: "assistant", Content: final})
		a.allHistory = append(a.allHistory, turn...)
		a.history = append(a.history, turn...)
		if len(a.history) > historyKeep {
			a.history = append([]llm.Message(nil), a.history[len(a.history)-historyKeep:]...)
		}
		a.histMu.Unlock()
		return nil
	}
	return fmt.Errorf("batas langkah penalaran (%d) tercapai sebelum SITREP final disusun — naikkan KARTIKA_MAX_STEPS jika diperlukan", a.cfg.MaxSteps)
}

// recordAborted merekam giliran yang dihentikan operator (Ctrl+C): arahan,
// pesan tersisip, dan catatan "ditangguhkan" masuk memori sesi sehingga
// prompt "lanjutkan" menuntaskan pekerjaan — lalu mengembalikan ErrAborted
// agar pemanggil KEMBALI KE PROMPT, bukan keluar program.
func (a *Agent) recordAborted(directive string, injected []llm.Message, view ChatUI) error {
	note := "CHAT DITANGGUHKAN oleh operator (ctrl+c) — arahan dan bukti yang sudah dikumpulkan tetap berlaku; lanjutkan pekerjaan saat diminta, susun SITREP hanya bila operator memintanya."
	a.histMu.Lock()
	turn := []llm.Message{{Role: "user", Content: directive}}
	turn = append(turn, injected...)
	turn = append(turn, llm.Message{Role: "assistant", Content: note})
	a.allHistory = append(a.allHistory, turn...)
	a.history = append(a.history, turn...)
	if len(a.history) > historyKeep {
		a.history = append([]llm.Message(nil), a.history[len(a.history)-historyKeep:]...)
	}
	a.histMu.Unlock()
	if view.OnHUD != nil {
		view.OnHUD()
	}
	return ErrAborted
}

// buildSystemPrompt menyusun doktrin + INDEKS SKILL dinamis: pustaka
// dipindai ulang setiap penugasan dimulai sehingga skill yang baru
// ditambahkan ke folder pustaka otomatis terpakai.
func buildSystemPrompt() string {
	tools.RefreshSkills()
	idx := tools.SkillIndex()
	prompt := systemPromptBase
	if idx != "" {
		prompt += "\n\nPUSTAKA SKILL TERPASANG (indeks dinamis — " + fmt.Sprint(tools.SkillsCount()) + " skill, skill baru otomatis tersedia):\n" + idx
		prompt += "\nGunakan indeks di atas untuk memilih skill_read yang relevan dengan fingerprint target."
	}
	return prompt
}

// ResetHistory mengosongkan memori percakapan (perintah /baru pada mode posko).
func (a *Agent) ResetHistory() {
	a.histMu.Lock()
	defer a.histMu.Unlock()
	a.history = nil
	a.allHistory = nil
}

// Snapshot menyalin seluruh giliran sesi (tanpa memotong) untuk disimpan
// ke file sesi — dipakai fitur simpan/buka-ulang sesi.
func (a *Agent) Snapshot() []llm.Message {
	a.histMu.Lock()
	defer a.histMu.Unlock()
	out := make([]llm.Message, len(a.allHistory))
	copy(out, a.allHistory)
	return out
}

// Restore mengisi memori percakapan dari sesi tersimpan.
func (a *Agent) Restore(msgs []llm.Message) {
	a.histMu.Lock()
	defer a.histMu.Unlock()
	a.allHistory = append([]llm.Message(nil), msgs...)
	trimmed := msgs
	if len(trimmed) > historyKeep {
		trimmed = trimmed[len(trimmed)-historyKeep:]
	}
	a.history = append([]llm.Message(nil), trimmed...)
}

// execTools mengeksekusi seluruh tool_calls dari engine, mencetak jalur
// fase + hasilnya ke HUD, dan menambahkan pesan tool ke msgs.
// Mengembalikan jumlah modul yang dijalankan.
func (a *Agent) execTools(msgs *[]llm.Message, resp llm.Message, seen map[string]bool) int {
	*msgs = append(*msgs, resp)
	n := 0
	for _, tc := range resp.ToolCalls {
		argsRaw := tc.Function.Arguments
		if strings.TrimSpace(argsRaw) == "" {
			argsRaw = "{}"
		}
		ui.PhaseForTool(tc.Function.Name, seen)
		ui.ToolLine(tc.Function.Name, compact(argsRaw))
		out := a.reg.Call(tc.Function.Name, json.RawMessage(argsRaw))
		n++
		if tc.Function.Name == "write_file" {
			a.noteWrite(argsRaw)
		}
		if strings.HasPrefix(out, "ERROR") {
			ui.ToolFail(firstLine(out))
		} else {
			ui.ToolOK(firstLine(out))
		}
		*msgs = append(*msgs, llm.Message{
			Role:       "tool",
			ToolCallID: tc.ID,
			Name:       tc.Function.Name,
			Content:    out,
		})
	}
	return n
}

func (a *Agent) noteWrite(argsJSON string) {
	var m map[string]any
	if json.Unmarshal([]byte(argsJSON), &m) == nil {
		if p, ok := m["path"].(string); ok && p != "" {
			a.lastWrite = p
		}
	}
}

func compact(s string) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(s)); err == nil {
		s = buf.String()
	}
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) > 120 {
		return string(r[:120]) + "…"
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > 140 {
		return string(r[:140]) + "…"
	}
	return s
}
