// Package ui menyediakan komponen Tactical HUD untuk terminal operator:
// logo, banner status, penanda fase, garis modul, dan bingkai SITREP.
package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

var useColor = true

// Sink keluaran HUD: bila dipasang (mode TUI layar penuh), seluruh baris
// HUD (fase, modul, ringkasan, peringatan) dialirkan ke sink alih-alih
// dicetak langsung ke stdout.
var sink func(string)

// SetSink memasang sink keluaran HUD (mode TUI); nil untuk kembali ke stdout.
func SetSink(f func(string)) { sink = f }

// out mencetak s (boleh multiline) via sink bila terpasang, atau stdout.
func out(s string) {
	if sink != nil {
		for _, l := range strings.Split(s, "\n") {
			sink(l)
		}
		return
	}
	fmt.Println(s)
}

// SlashCommand adalah satu entri menu saran perintah slash.
type SlashCommand struct {
	Cmd  string
	Desc string
}

// isLocalMode menentukan konfigurasi menu saran slash (lokal vs API/cloud).
var isLocalMode = true

func buildSlashCommands(local bool) []SlashCommand {
	var cmds []SlashCommand
	if local {
		cmds = append(cmds,
			SlashCommand{"/pilih model", "pilih / ganti model AI (KARTIKA_MODEL)"},
			SlashCommand{"/konfigurasi-api-key", "konfigurasi lengkap: URL, API Key & Model"},
			SlashCommand{"/konfigurasi-model-lokal", "wizard engine LOKAL (Ollama/vLLM/LM Studio) tanpa API key"},
		)
	} else {
		cmds = append(cmds,
			SlashCommand{"/konfigurasi-api-key", "konfigurasi lengkap: URL, API Key & Model"},
			SlashCommand{"/pilih model", "pilih / ganti model AI (KARTIKA_MODEL)"},
			SlashCommand{"/konfigurasi-model-lokal", "wizard engine LOKAL (Ollama/vLLM/LM Studio) tanpa API key"},
		)
	}
	cmds = append(cmds,
		SlashCommand{"/status", "info engine & modul aktif"},
		SlashCommand{"/modul", "daftar seluruh modul taktis"},
		SlashCommand{"/skill", "daftar & baca pustaka skill metodologi (/skill <nama>)"},
		SlashCommand{"/mcp", "status MCP server & lokasi konfigurasi"},
		SlashCommand{"/pikir", "tampilkan/sembunyikan pemikiran model (ctrl+b)"},
		SlashCommand{"/bersih", "bersihkan layar dan tampilkan ulang banner"},
		SlashCommand{"/baru", "sesi baru — hapus memori percakapan"},
		SlashCommand{"/update", "perbarui kartika — cek rilis repo, pasang binary/checksum atau build ulang"},
		SlashCommand{"/bantuan", "tampilkan bantuan"},
		SlashCommand{"/keluar", "tutup sesi posko"},
	)
	return cmds
}

// SlashCommands adalah daftar perintah mode posko untuk menu saran & bantuan.
var SlashCommands = buildSlashCommands(true)

// SetLocalMode mengatur apakah menu saran slash menampilkan perintah mode lokal
// (/pilih model) atau mode API/cloud (/masukan-api-key).
func SetLocalMode(local bool) {
	isLocalMode = local
	SlashCommands = buildSlashCommands(local)
}

// SetColor mengaktifkan/menonaktifkan keluaran ANSI.
func SetColor(v bool) { useColor = v }

// DetectColor menonaktifkan warna bila --no-color, NO_COLOR di-set,
// atau stdout bukan terminal (mis. saat dipakai di CI/CD pipeline).
func DetectColor(noColor bool) bool {
	if noColor {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if fi, err := os.Stdout.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
		return false
	}
	return true
}

func c(code, s string) string {
	if !useColor {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Bold mempertebangkan teks.
func Bold(s string) string { return c("1", s) }

// Dim menampilkan teks redup (detail sekunder).
func Dim(s string) string { return c("90", s) }

// Red untuk status gagal / temuan KRITIS.
func Red(s string) string { return c("31", s) }

// Green untuk status OK / ONLINE.
func Green(s string) string { return c("32", s) }

// Yellow untuk peringatan / temuan WASPADA.
func Yellow(s string) string { return c("33", s) }

// Cyan untuk elemen HUD utama.
func Cyan(s string) string { return c("36", s) }

// Sel menandai elemen yang sedang disorot (hover/pilihan) dengan video
// terbalik — dipakai tab header & baris menu agar kursor mouse terasa hidup.
func Sel(s string) string { return c("7", s) }

// Underline memberi garis bawah — dipakai tab AKTIF saat kursor melintas,
// sebab tab aktif sudah berwarna tegas sehingga sorotan video-terbalik
// tidak akan terlihat bedanya.
func Underline(s string) string { return c("4", s) }

// Magenta untuk penanda fase operasi.
func Magenta(s string) string { return c("35", s) }

const logo = `██╗  ██╗ █████╗ ██████╗ ████████╗██╗ ██╗  ██╗ █████╗
██║ ██╔╝██╔══██╗██╔══██╗╚══██╔══╝██║██║ ██╔╝██╔══██╗
█████╔╝ ███████║██████╔╝   ██║   ██║█████╔╝ ███████║
██╔═██╗ ██╔══██║██╔══██╗   ██║   ██║██╔═██╗ ██╔══██║
██║  ██╗██║  ██║██║  ██╗   ██║   ██║██║  ██╗██║  ██║
╚═╝  ╚═╝╚═╝  ╚═╝╚═╝  ╚═╝   ╚═╝   ╚═╝╚═╝  ╚═╝╚═╝  ╚═╝`

// PrintHeader mencetak logo + header TACTICAL CORE.
func PrintHeader(v string) {
	fmt.Println(Dim(logo))
	fmt.Println(Bold(Cyan("[KARTIKA-AGENT // TACTICAL CORE v" + v + "]")))
}

// PrintStatus mencetak baris status sesuai format briefing. engineUp
// melaporkan hasil ping awal ke endpoint engine (ONLINE / TAK TERHUBUNG).
func PrintStatus(model string, builtin, mcp int, engineUp bool) {
	st := Bold(Green("ONLINE"))
	if !engineUp {
		st = Bold(Red("ENGINE TAK TERHUBUNG"))
	}
	mod := fmt.Sprintf("%d Built-in + %d MCP", builtin, mcp)
	fmt.Printf("STATUS : %s | ENGINE: %s | MODUL AKTIF: %s\n", st, Bold(model), Bold(mod))
	fmt.Println()
}

// PrintMissionLine mencetak direktif chat (mode senyap/headless).
func PrintMissionLine(d string) {
	fmt.Printf("%s %s\n", Bold(Cyan("CHAT>")), d)
}

// Prompt mengembalikan string prompt mode posko.
func Prompt(s string) string { return Bold(Cyan(s)) }

// IsTTY melaporkan apakah stdout adalah terminal interaktif (bukan pipe/CI).
func IsTTY() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
}

// ---------------------------------------------------------------------------
// Spinner — indikator kerja animasi saat menunggu engine (seperti "Thinking"
// pada Claude Code). Otomatis nonaktif pada terminal non-interaktif.
// ---------------------------------------------------------------------------

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner adalah indikator kerja yang berjalan di goroutine terpisah.
type Spinner struct {
	stop chan struct{}
	done chan struct{}
	on   bool
	hint string // baris petunjuk tambahan di bawah spinner (mis. tombol ctrl+b)
}

// spinnerInert menonaktifkan spinner (mode TUI — status bar yang menunjukkan).
var spinnerInert bool

// SetSpinnerInert mematikan spinner (dipakai mode TUI layar penuh).
func SetSpinnerInert(v bool) { spinnerInert = v }

// NewSpinner memulai spinner dengan label tertentu. Gunakan Stop() untuk
// mengakhirinya (aman dipanggil berkali-kali).
func NewSpinner(label string) *Spinner { return NewSpinnerHint(label, "") }

// NewSpinnerHint sama dengan NewSpinner, namun menampilkan satu baris
// petunjuk redup di bawah spinner (mis. "ctrl+b ...") selama engine berpikir.
// Baris petunjuk DITAMPILKAN SEGERA saat mulai (bukan menunggu tick) dan
// dibersihkan otomatis saat Stop().
func NewSpinnerHint(label, hint string) *Spinner {
	s := &Spinner{stop: make(chan struct{}), done: make(chan struct{}), hint: hint}
	if !useColor || !IsTTY() || spinnerInert {
		close(s.done)
		return s
	}
	s.on = true
	start := time.Now()
	// Gambar frame pertama + petunjuk SEKARANG agar petunjuk tidak hilang
	// bila reasoning token pertama tiba lebih cepat dari tick 90 ms.
	fmt.Printf("\r\x1b[2K  %s %s %s", Cyan(spinnerFrames[0]), Dim(label), Dim("(0s)"))
	if hint != "" {
		fmt.Println()
		fmt.Println("  " + Dim(hint))
		fmt.Print("\x1b[1A") // kembali ke baris spinner
	}
	go func() {
		defer close(s.done)
		t := time.NewTicker(90 * time.Millisecond)
		defer t.Stop()
		i := 1
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				ela := int(time.Since(start).Seconds())
				fmt.Printf("\r\x1b[2K  %s %s %s", Cyan(spinnerFrames[i%len(spinnerFrames)]), Dim(label), Dim(fmt.Sprintf("(%ds)", ela)))
				i++
			}
		}
	}()
	return s
}

// Stop menghentikan spinner dan membersihkan barisnya (termasuk baris
// petunjuk bila ada).
func (s *Spinner) Stop() {
	if !s.on {
		return
	}
	s.on = false
	close(s.stop)
	<-s.done
	if useColor {
		if s.hint != "" {
			// Bersihkan baris petunjuk di bawah baris spinner lalu kembali.
			fmt.Print("\r\x1b[2K\x1b[1B\r\x1b[2K\x1b[1A")
		}
		fmt.Print("\r\x1b[2K")
	}
}

// ---------------------------------------------------------------------------
// Kotak input gaya chat — menggambar bingkai rounded di sekitar area input,
// seperti kotak prompt Claude Code / opencode.
// ---------------------------------------------------------------------------

// termWidth mengembalikan lebar terminal (COLUMNS, stty size, atau 80).
func termWidth() int {
	if cv := os.Getenv("COLUMNS"); cv != "" {
		if n, err := strconv.Atoi(cv); err == nil && n > 0 {
			return clampWidth(n)
		}
	}
	if out, err := exec.Command("stty", "size").Output(); err == nil {
		f := strings.Fields(string(out))
		if len(f) == 2 {
			if w, err := strconv.Atoi(f[1]); err == nil && w > 0 {
				return clampWidth(w)
			}
		}
	}
	return 80
}

func clampWidth(w int) int {
	if w < 44 {
		return 44
	}
	if w > 100 {
		return 100
	}
	return w
}

// InputBox menggambar sisi atas kotak input chat (bingkai atas + prompt di
// dalamnya) dan mengembalikan baris penutup yang harus dicetak setelah
// operator menekan Enter (pakai InputBoxEnd). Pada terminal non-interaktif
// hanya prompt polos yang dicetak dan penutup berupa string kosong.
func InputBox(prefix string) (closeLine string) {
	if !useColor || !IsTTY() {
		fmt.Print(Prompt(prefix + " "))
		return ""
	}
	w := termWidth()
	top := "╭" + strings.Repeat("─", w-2) + "╮"
	bottom := "╰" + strings.Repeat("─", w-2) + "╯"
	fmt.Println(Cyan(top))
	fmt.Print("│ " + Bold(Cyan(prefix+" ")))
	return Cyan(bottom)
}

// InputBoxEnd mencetak baris penutup kotak input setelah operator menekan Enter.
func InputBoxEnd(closeLine string) {
	if closeLine != "" {
		fmt.Println()
		fmt.Println(closeLine)
	}
}

// TurnRule mencetak garis pemisah giliran obrolan, contoh: ── KARTIKA ─────
func TurnRule(who string) {
	label := "─ " + who + " "
	w := termWidth()
	barLen := w - len(label) - 2
	if barLen < 4 {
		barLen = 4
	}
	fmt.Println(Dim(label + strings.Repeat("─", barLen)))
}

// Phase mencetak penanda fase operasi, contoh: [FASE 1: RECON]  Memetakan ...
func Phase(n int, label, desc string) {
	tag := fmt.Sprintf("[FASE %d: %s]", n, label)
	for len(tag) < 17 {
		tag += " "
	}
	out(fmt.Sprintf("%s %s", Bold(Magenta(tag)), desc))
}

// PhaseForTool memetakan modul ke fase operasinya dan mencetak header fase
// sekali per fase untuk setiap misi.
func PhaseForTool(tool string, seen map[string]bool) {
	var n int
	var label, desc string
	switch tool {
	case "scan_dir", "http_probe":
		n, label, desc = 1, "RECON", "Memetakan struktur target & mengambil profil awal ..."
	case "write_file":
		n, label, desc = 3, "REPORT", "Menyusun SITREP dan rekomendasi hardening konfigurasi ..."
	default: // read_file + run_cmd + seluruh modul MCP
		n, label, desc = 2, "VERIFY", "Memvalidasi temuan: telusur kode, probe, dan uji aktif untuk membangun PoC ..."
	}
	if seen[label] {
		return
	}
	seen[label] = true
	Phase(n, label, desc)
}

// ToolLine mencetak eksekusi modul, contoh: [MODUL] http_probe({"url": ...})
func ToolLine(name, args string) {
	out(fmt.Sprintf("  %s %s(%s)", Cyan("[MODUL]"), Bold(name), Dim(args)))
}

// ToolOK mencetak ringkasan hasil modul yang sukses.
func ToolOK(preview string) { out(fmt.Sprintf("  %s %s", Green("[SELESAI]"), Dim(preview))) }

// warnaiBaris menambahkan warna pada satu baris utuh keluaran model:
// judul markdown → cyan tebal, severity temuan → warna berdasarkan
// tingkatnya, status coverage → hijau/kuning/redup.
func warnaiBaris(ln string) string {
	t := strings.TrimSpace(ln)
	if t == "" {
		return ln
	}
	// Judul markdown (SITREP/temuan) → cyan tebal.
	if strings.HasPrefix(ln, "#") {
		return Bold(Cyan(ln))
	}
	// Garis bingkai panjang → redup.
	if len(t) > 12 && (strings.HasPrefix(t, "===") || strings.HasPrefix(t, "---")) {
		return Dim(ln)
	}
	ln = strings.ReplaceAll(ln, "[INFO]", Dim("[INFO]"))
	ln = strings.ReplaceAll(ln, "KRITIS", Bold(Red("KRITIS")))
	ln = strings.ReplaceAll(ln, "CRITICAL", Bold(Red("CRITICAL")))
	ln = strings.ReplaceAll(ln, "TINGGI", Red("TINGGI"))
	ln = strings.ReplaceAll(ln, "SEDANG", Yellow("SEDANG"))
	ln = strings.ReplaceAll(ln, "MEDIUM", Yellow("MEDIUM"))
	ln = strings.ReplaceAll(ln, "RENDAH", Green("RENDAH"))
	ln = strings.ReplaceAll(ln, "LOW", Green("LOW"))
	// Status coverage (bagian COVERAGE SITREP).
	ln = strings.ReplaceAll(ln, "TERBUKTI", Bold(Red("TERBUKTI")))
	ln = strings.ReplaceAll(ln, "BERSIH", Green("BERSIH"))
	ln = strings.ReplaceAll(ln, "TERBLOKIR", Yellow("TERBLOKIR"))
	ln = strings.ReplaceAll(ln, "BELUM", Dim("BELUM"))
	return ln
}

// deltaColorizer mewarnai aliran teks model secara bertahap: chunk token
// sering memotong di tengah baris, jadi baris ditahan hingga utuh lalu
// diwarna; baris parsial terakhir lolos apa adanya — teks tidak pernah
// hilang, pewarnaan menyusul saat baris selesai.
type deltaColorizer struct {
	buf strings.Builder
}

// Colorize menerima chunk baru dan mengembalikan teks siap cetak/append.
func (d *deltaColorizer) Colorize(chunk string) string {
	d.buf.WriteString(chunk)
	s := d.buf.String()
	idx := strings.LastIndex(s, "\n")
	if idx < 0 {
		return chunk // belum ada baris utuh — pass-through
	}
	d.buf.Reset()
	d.buf.WriteString(s[idx+1:])
	var b strings.Builder
	for _, ln := range strings.SplitAfter(s[:idx+1], "\n") {
		if ln == "" {
			continue
		}
		if strings.HasSuffix(ln, "\n") {
			b.WriteString(warnaiBaris(strings.TrimSuffix(ln, "\n")))
			b.WriteString("\n")
		} else {
			b.WriteString(ln) // baris parsial — apa adanya
		}
	}
	return b.String()
}

// NewDeltaColorizer membangun pewarnai aliran keluaran model (satu instance
// per giliran chat; memegang buffer baris parsial antar chunk).
func NewDeltaColorizer() func(string) string {
	d := &deltaColorizer{}
	return d.Colorize
}

// ToolFail mencetak ringkasan modul yang gagal.
func ToolFail(preview string) { out(fmt.Sprintf("  %s %s", Red("[GAGAL]"), Red(preview))) }

// SitrepFrame membungkus SITREP final dengan bingkai "=" sesuai format briefing.
func SitrepFrame(content string) {
	bar := strings.Repeat("=", 68)
	out("")
	out(bar)
	out(content)
	out(bar)
}

// Info mencetak teks biasa.
func Info(s string) { out(s) }

// Warn mencetak peringatan.
func Warn(s string) { out(Yellow("[PERINGATAN] " + s)) }

// Fail mencetak pesan kegagalan fatal.
func Fail(s string) { out(Red(s)) }

// Debug mencetak pesan diagnostik redup.
func Debug(s string) { out("  " + Dim(s)) }

// MCPOnline mencetak status MCP server yang berhasil handshake.
func MCPOnline(name, server, ver string, tools int) {
	fmt.Printf("  %s %s ONLINE — server: %s v%s — %d tools\n", Green("[MCP]"), Bold(name), server, ver, tools)
}

// MCPFail mencetak status MCP server yang gagal dimuat.
func MCPFail(name, reason string) {
	fmt.Printf("  %s %s GAGAL — %s\n", Red("[MCP]"), Bold(name), reason)
}

// PrintPoskoHelp mencetak bantuan mode posko (chat taktis interaktif).
func PrintPoskoHelp() {
	fmt.Println()
	fmt.Println(Bold(Cyan("MODE POSKO — CHAT TAKTIS (gaya Claude Code / opencode)")))
	for _, c := range SlashCommands {
		fmt.Printf("  %-16s: %s\n", c.Cmd, c.Desc)
	}
	fmt.Println("  <teks>          : chat / pertanyaan — jawaban mengalir real-time")
	fmt.Println(Dim("  Ketik '/' untuk memunculkan menu perintah (▲▼ pilih, Tab lengkapi)."))
	fmt.Println(Dim("  ctrl+b pemikiran model · PgUp/PgDn scroll · ctrl+t tab baru · alt+1..9 pindah tab."))
	fmt.Println(Dim("  Sesi tersimpan otomatis — kartika --lanjut [id] buka ulang, kartika -s daftar sesi."))
	fmt.Println(Dim("  ctrl+b : memunculkan pemikiran model di latar belakang ke depan —"))
	fmt.Println(Dim("           tekan lagi untuk menyembunyikannya; berlaku kapan saja."))
	fmt.Println(Dim("  Catatan: perintah tanpa garis miring (status, modul, dst.) tetap dikenali;"))
	fmt.Println(Dim("          konteks percakapan diingat antar chat selama sesi."))
	fmt.Println(Dim("  Sesi otomatis tersimpan saat keluar — `kartika --lanjut` membuka ulang."))
	fmt.Println()
}

// PrintConfigShort mencetak pengingat satu-baris setelah panduan lengkap
// pernah ditampilkan (agar pesan konfigurasi tidak berulang-ulang).
func PrintConfigShort() {
	fmt.Println(Yellow("[KONFIGURASI] API/model belum dikonfigurasi — isi KARTIKA_API_URL, KARTIKA_API_KEY, KARTIKA_MODEL di ~/.config/kartika/.env (panduan lengkap sudah tampil di atas)."))
	fmt.Println()
}

// PrintConfigHelp menampilkan panduan memasukkan API key / model lengkap
// dengan lokasi file konfigurasinya, dipakai saat chat gagal karena
// engine tidak terhubung, kunci ditolak, atau model tidak ditemukan.
func PrintConfigHelp(engineURL, model, reason string) {
	fmt.Println()
	fmt.Println(Red("[KONFIGURASI] " + reason))
	fmt.Printf("  Engine aktif : %s\n", Dim(engineURL))
	fmt.Printf("  Model aktif  : %s\n", Dim(model))
	fmt.Println()
	fmt.Println(Bold("  Tempat memasukkan API / model (pilih salah satu):"))
	fmt.Println("    1. ~/.config/kartika/.env    — konfigurasi global (disarankan)")
	fmt.Println("    2. ./.env di folder tempat kartika dijalankan — per proyek")
	fmt.Println()
	fmt.Println(Bold("  Isi file dengan tiga variabel berikut:"))
	fmt.Println()
	fmt.Println("    KARTIKA_API_URL=https://api.groq.com/openai/v1")
	fmt.Println("    KARTIKA_API_KEY=gsk_xxxxxxxxxxxx      <- kunci API Anda")
	fmt.Println("    KARTIKA_MODEL=llama-3.3-70b-versatile")
	fmt.Println()
	fmt.Println(Dim("  Contoh format kunci lain: openai (sk-...), openrouter (sk-or-v1-...),"))
	fmt.Println(Dim("  gemini (AIza...), nvidia NIM (nvapi-...), ollama/vLLM lokal."))
	fmt.Println(Dim("  Tanpa file konfigurasi, bisa langsung dengan profil bawaan:"))
	fmt.Println()
	fmt.Println("    kartika --provider groq")
	fmt.Println(Dim("  Profil tersedia: ollama | vllm | groq | openai | openrouter | gemini | nvidia"))
	fmt.Println(Dim("  Cek konfigurasi aktif kapan saja dengan perintah /status"))
	fmt.Println()
}

// liveInputHandlers dipasang dari main: render menampilkan buffer ketikan
// live (di bar input TUI), submit mengirim teks saat Enter (ke antrian
// model), abort menghentikan giliran chat saat Ctrl+C. Dipakai oleh KeyTap
// (platform unix); di platform tanpa mode raw tinggal tidak aktif.
var (
	liveRender func(string)
	liveSubmit func(string)
	liveAbort  func()
)

// SetLiveInputHandlers memasang perilaku input-live selama engine bekerja:
// operator tetap bisa mengetik saat engine berjalan, menempel teks (paste)
// tanpa menjalankan apa pun, dan menghentikan giliran chat dengan Ctrl+C.
//
//	render: dipanggil tiap buffer berubah ("" = kosong);
//	submit: dipanggil saat Enter dengan teks yang diketik;
//	abort : dipanggil saat Ctrl+C — menghentikan giliran chat berjalan.
func SetLiveInputHandlers(render func(string), submit func(string), abort func()) {
	liveRender, liveSubmit, liveAbort = render, submit, abort
}
