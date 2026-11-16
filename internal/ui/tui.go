//go:build linux || darwin

// tui.go — mode layar penuh (alternate screen) gaya opencode: kartika
// membuka "terminal"-nya sendiri — bingkai atas, jendela transkrip chat
// yang mengikuti ekor, menu saran slash, bar status, dan baris input di
// bawah. Keluar (Ctrl+C / /keluar / EOF) mengembalikan layar semula.
package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var altActive bool

// EnterAlt membuka layar alternatif (seperti aplikasi TUI penuh).
// Mode mouse (klik + hover + scroll sentuh) diaktifkan bersamaan.
func EnterAlt() {
	if !useColor || !IsTTY() {
		return
	}
	altActive = true
	fmt.Print("\x1b[?1049h\x1b[H\x1b[2J")
	// 1000 = klik, 1003 = hover semua gerakan, 1006 = encoding SGR
	fmt.Print("\x1b[?1000h\x1b[?1003h\x1b[?1006h")
}

// ExitAlt menutup layar alternatif dan mengembalikan konten lama.
// Tidak berpengaruh bila TUI tidak aktif.
func ExitAlt() {
	if !altActive {
		return
	}
	altActive = false
	fmt.Print("\x1b[?1006l\x1b[?1003l\x1b[?1000l") // matikan mouse
	fmt.Print("\x1b[?1049l")
}

// TUIAvailable melaporkan apakah mode layar penuh tersedia.
func TUIAvailable() bool { return useColor && IsTTY() }

// winsize adalah struktur ioctl TIOCGWINSZ.
type winsize struct {
	Row, Col, Xpixel, Ypixel uint16
}

// termSize mengembalikan (kolom, baris) terminal — via ioctl langsung
// (tanpa subprocess, dipanggil tiap render sehingga resize selalu ikut).
func termSize() (w, h int) {
	ws := &winsize{}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(os.Stdin.Fd()),
		uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(ws)))
	if errno == 0 && ws.Col > 0 && ws.Row > 0 {
		return int(ws.Col), int(ws.Row)
	}
	w, h = 80, 24
	if cv := os.Getenv("COLUMNS"); cv != "" {
		if n, err := strconvAtoi(cv); err == nil && n > 0 {
			w = n
		}
	}
	if cv := os.Getenv("LINES"); cv != "" {
		if n, err := strconvAtoi(cv); err == nil && n > 0 {
			h = n
		}
	}
	return w, h
}

func strconvAtoi(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, io.ErrUnexpectedEOF
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, io.ErrUnexpectedEOF
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

// rxEsc mencocokkan satu urutan escape ANSI CSI.
var rxEsc = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// visibleLen panjang tampak string tanpa kode ANSI.
func visibleLen(s string) int { return len([]rune(rxEsc.ReplaceAllString(s, ""))) }

// wrapANSI melipat satu baris (boleh ber-ANSI) menjadi baris ≤w kolom tampak.
// Baris polos dilipat pada batas kata; baris ber-ANSI (HUD pendek) dilipat keras.
func wrapANSI(s string, w int) []string {
	if w < 20 {
		w = 20
	}
	var lines []string
	for _, raw := range strings.Split(s, "\n") {
		if strings.ContainsRune(raw, 0x1b) {
			lines = append(lines, wrapHard(raw, w)...)
			continue
		}
		lines = append(lines, wrapWords(raw, w)...)
	}
	return lines
}

// wrapWords melipat teks polos pada spasi (word wrap), kata super panjang
// dipotong keras.
func wrapWords(s string, w int) []string {
	var out []string
	var cur strings.Builder
	curLen := 0
	for _, word := range strings.Split(s, " ") {
		wl := len([]rune(word))
		for wl > w { // kata melebihi lebar: potong keras
			if curLen > 0 {
				out = append(out, cur.String())
				cur.Reset()
				curLen = 0
			}
			r := []rune(word)
			out = append(out, string(r[:w]))
			word = string(r[w:])
			wl = len([]rune(word))
		}
		if curLen == 0 {
			cur.WriteString(word)
			curLen = wl
			continue
		}
		if curLen+1+wl <= w {
			cur.WriteString(" " + word)
			curLen += 1 + wl
		} else {
			out = append(out, cur.String())
			cur.Reset()
			cur.WriteString(word)
			curLen = wl
		}
	}
	out = append(out, cur.String())
	return out
}

// wrapHard melipat keras per kolom tampak, menyelipkan urutan ANSI apa adanya.
func wrapHard(s string, w int) []string {
	runes := []rune(s)
	var lines []string
	var cur strings.Builder
	width := 0
	i := 0
	for i < len(runes) {
		if runes[i] == 0x1b { // salin seluruh urutan escape apa adanya
			j := i + 1
			for j < len(runes) && (runes[j] == '[' || (runes[j] >= '0' && runes[j] <= '9') || runes[j] == ';') {
				j++
			}
			if j < len(runes) && runes[j] != 0x1b {
				j++ // huruf akhiran (m/A/K/dst.)
			}
			cur.WriteString(string(runes[i:j]))
			i = j
			continue
		}
		cur.WriteRune(runes[i])
		width++
		i++
		if width >= w {
			lines = append(lines, cur.String())
			cur.Reset()
			width = 0
		}
	}
	lines = append(lines, cur.String())
	return lines
}

// tabBuf adalah isi satu tab sesi (multi-tab ala opencode).
type tabBuf struct {
	lines   []string // transkrip mentah (dilipat saat render sesuai lebar)
	scroll  int      // geser pandang dari ekor (0 = ikuti ekor)
	status  string   // bar status tab ini
	live    string   // pesan yang sedang mengalir (ekor transkrip)
	liveDim bool
	title   string // label tab pada header (mis. id sesi)
}

// TUI adalah layar penuh posko: tab + transkrip + status + input + menu slash.
type TUI struct {
	mu         sync.Mutex
	w, h       int
	tabs       []*tabBuf
	curIdx     int
	thinkOn    bool // animasi "engine berpikir" pada bar status
	thinkLabel string
	thinkFrame int
	thinkStop  chan struct{}
	liveInput  string // teks yang diketik operator SAAT engine bekerja (live)
	prefix     string
	buf        []rune
	menu       []SlashCommand
	sel        int
	lastRender time.Time
	pend       bool
	onTab      func(idx int) // dipanggil saat tab baru dibuat / tab dipindah
	onClose    func(idx int) // dipanggil saat tab ditutup (sebelum onTab)

	// peta zona klik header (dipusat saat build): area tab & tombol
	headerZones  []hdrZone
	menuBaseY    int // baris layar 1-based dari baris menu pertama
	menuOff      int // jendela scroll menu slash / picker
	menuWinStart int // index penuh baris menu pertama yang terlihat
	menuWinRows  int // jumlah baris menu yang terlihat
}

// hdrZone adalah area klik di bar header: tab (pindah), add (+tab), close (×).
type hdrZone struct {
	start, end int  // kolom 1-based inklusif
	kind       byte // 't' tab, 'a' add, 'c' close
	tab        int  // index tab (untuk kind 't')
}

var activeTUI *TUI // referensi untuk KeyTap (mouse saat engine bekerja)

// NewTUI membangun layar penuh (panggil setelah EnterAlt) dengan satu tab.
func NewTUI() *TUI {
	w, h := termSize()
	t := &TUI{w: w, h: h, tabs: []*tabBuf{{}}}
	activeTUI = t
	return t
}

// cur mengembalikan buffer tab aktif (panggil sambil memegang mutex).
func (t *TUI) cur() *tabBuf { return t.tabs[t.curIdx] }

// AddTab membuat tab baru lalu pindah ke sana.
func (t *TUI) AddTab() {
	t.mu.Lock()
	t.tabs = append(t.tabs, &tabBuf{})
	t.curIdx = len(t.tabs) - 1
	idx := t.curIdx
	f := t.onTab
	t.mu.Unlock()
	if f != nil {
		f(idx)
	}
	t.doRender()
}

// SwitchTab pindah ke tab i (0-based); di luar rentang = no-op.
func (t *TUI) SwitchTab(i int) {
	t.mu.Lock()
	if i < 0 || i >= len(t.tabs) || i == t.curIdx {
		t.mu.Unlock()
		return
	}
	t.curIdx = i
	idx := t.curIdx
	f := t.onTab
	t.mu.Unlock()
	if f != nil {
		f(idx)
	}
	t.doRender()
}

// SetTabHook memasang callback yang dipanggil saat tab baru dibuat atau
// pandang berpindah tab (argumen: index tab aktif).
func (t *TUI) SetTabHook(f func(idx int)) {
	t.mu.Lock()
	t.onTab = f
	t.mu.Unlock()
}

// SetTabCloseHook memasang callback yang dipanggil saat tab ditutup
// (argumen: index tab yang dihapus — sebelum perpindahan).
func (t *TUI) SetTabCloseHook(f func(idx int)) {
	t.mu.Lock()
	t.onClose = f
	t.mu.Unlock()
}

// CloseTab menutup tab i (0-based). Tab terakhir tidak boleh ditutup —
// kembalikan false. Setelah ditutup, callback onClose(i) lalu onTab(idxBaru)
// dipanggil agar status utama ikut menyinkronkan keadaannya.
func (t *TUI) CloseTab(i int) bool {
	t.mu.Lock()
	if i < 0 || i >= len(t.tabs) || len(t.tabs) <= 1 {
		t.mu.Unlock()
		return false
	}
	t.tabs = append(t.tabs[:i], t.tabs[i+1:]...)
	if t.curIdx >= len(t.tabs) {
		t.curIdx = len(t.tabs) - 1
	} else if t.curIdx > i {
		t.curIdx--
	}
	idx := t.curIdx
	fTab, fClose := t.onTab, t.onClose
	t.mu.Unlock()
	if fClose != nil {
		fClose(i)
	}
	if fTab != nil {
		fTab(idx)
	}
	t.doRender()
	return true
}

// SetTabTitle mengganti label tab aktif pada header.
func (t *TUI) SetTabTitle(s string) {
	t.mu.Lock()
	t.cur().title = s
	t.mu.Unlock()
	t.doRender()
}

// Current melaporkan index tab aktif.
func (t *TUI) Current() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.curIdx
}

// Resize membaca ulang ukuran terminal lalu menggambar ulang (SIGWINCH).
func (t *TUI) Resize() {
	t.mu.Lock()
	t.w, t.h = termSize()
	t.mu.Unlock()
	t.doRender()
}

// OnResize memasang pemantau SIGWINCH (Linux/macOS).
func OnResize(f func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	go func() {
		for range ch {
			f()
		}
	}()
}

// Append menambah baris transkrip (boleh multiline & ber-ANSI). Bila
// operator sedang menahan pandang ke atas (scroll>0), offset ikut digeser
// agar konten yang dilihat tidak berlari.
func (t *TUI) Append(line string) {
	t.mu.Lock()
	b := t.cur()
	b.lines = append(b.lines, line)
	if b.scroll > 0 { // hanya jaga posisi bila operator menahan pandang
		b.scroll += len(wrapANSI(line, t.w-2))
	}
	b.live, b.liveDim = "", false
	t.mu.Unlock()
	t.doRender()
}

// Clear mengosongkan transkrip (perintah /bersih).
func (t *TUI) Clear() {
	t.mu.Lock()
	b := t.cur()
	b.lines, b.live, b.liveDim = nil, "", false
	b.scroll = 0
	t.mu.Unlock()
	t.doRender()
}

// SetStatus mengganti baris status (di atas input).
func (t *TUI) SetStatus(s string) {
	t.mu.Lock()
	t.cur().status = s
	t.mu.Unlock()
	t.doRender()
}

// SetLiveInput menampilkan/menyembunyikan teks yang diketik operator SAAT
// engine bekerja (live input di bar input: Enter = kirim ke model).
func (t *TUI) SetLiveInput(s string) {
	t.mu.Lock()
	t.liveInput = s
	t.mu.Unlock()
	t.doRender()
}

// SetThinking menyalakan/mematikan animasi "engine berpikir" pada bar status.
func (t *TUI) SetThinking(on bool, label string) {
	t.mu.Lock()
	t.thinkLabel = label
	if on && t.thinkStop == nil {
		stop := make(chan struct{})
		t.thinkStop = stop
		go func() {
			tick := time.NewTicker(90 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-stop:
					return
				case <-tick.C:
					t.mu.Lock()
					t.thinkFrame++
					scr := t.build()
					t.mu.Unlock()
					fmt.Print(scr)
				}
			}
		}()
	}
	if !on && t.thinkStop != nil {
		close(t.thinkStop)
		t.thinkStop = nil
	}
	t.thinkOn = on
	if !on {
		t.thinkFrame = 0
	}
	t.mu.Unlock()
	t.doRender()
}

// Scroll menggeser pandang transkrip (delta negatif = ke atas). 0 = ikuti ekor.
func (t *TUI) Scroll(delta int) {
	t.mu.Lock()
	b := t.cur()
	max := len(b.lines)
	if b.scroll+delta < 0 {
		b.scroll = 0
	} else if b.scroll+delta > max {
		b.scroll = max
	} else {
		b.scroll += delta
	}
	scr := t.build()
	t.mu.Unlock()
	fmt.Print(scr)
}

// ScrollTo mengatur posisi pandang langsung (0 = ekor).
func (t *TUI) ScrollTo(pos int) {
	t.mu.Lock()
	t.cur().scroll = pos
	scr := t.build()
	t.mu.Unlock()
	fmt.Print(scr)
}

// SetLive menampilkan pesan yang sedang mengalir tanpa mengcommit-nya.
func (t *TUI) SetLive(s string, dim bool) {
	t.mu.Lock()
	b := t.cur()
	b.live, b.liveDim = s, dim
	t.mu.Unlock()
	t.renderThrottled()
}

// CommitLive menetapkan pesan yang mengalir menjadi baris transkrip.
func (t *TUI) CommitLive() {
	t.mu.Lock()
	b := t.cur()
	l, d := b.live, b.liveDim
	b.live, b.liveDim = "", false
	if strings.TrimSpace(l) != "" {
		if d {
			l = Dim(l)
		}
		b.lines = append(b.lines, l)
		if b.scroll > 0 { // hanya jaga posisi bila operator menahan pandang
			b.scroll += len(wrapANSI(l, t.w-2))
		}
	}
	t.mu.Unlock()
	t.doRender()
}

// SetInput dipanggil editor input setiap perubahan buffer/menu.
func (t *TUI) SetInput(prefix string, buf []rune, menu []SlashCommand, sel int) {
	t.mu.Lock()
	if !slicesEqual(menu, t.menu) { // menu baru → reset jendela scroll
		t.menuOff = 0
	}
	t.prefix = prefix
	t.buf = append([]rune(nil), buf...)
	t.menu = menu
	t.sel = sel
	t.mu.Unlock()
	t.doRender()
}

// ScrollMenu menggeser jendela menu slash/picker (delta ±1 per gerakan
// wheel). Batas dijaga saat build.
func (t *TUI) ScrollMenu(delta int) {
	t.mu.Lock()
	t.menuOff += delta
	if t.menuOff < 0 {
		t.menuOff = 0
	}
	if t.menuOff > len(t.menu) {
		t.menuOff = len(t.menu)
	}
	t.mu.Unlock()
	t.doRender()
}

func slicesEqual(a, b []SlashCommand) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// doRender menggambar seluruh layar (throttle 40 ms via renderThrottled).
// Ukuran terminal dibaca ulang tiap render (ioctl murah) sehingga resize
// (mis. maximize) selalu mengikuti.
func (t *TUI) doRender() {
	t.mu.Lock()
	if w, h := termSize(); w > 0 && h > 0 && (w != t.w || h != t.h) {
		t.w, t.h = w, h
	}
	scr := t.build()
	t.lastRender = time.Now()
	t.mu.Unlock()
	fmt.Print(scr)
}

func (t *TUI) renderThrottled() {
	t.mu.Lock()
	if time.Since(t.lastRender) < 40*time.Millisecond {
		if !t.pend {
			t.pend = true
			go func() {
				time.Sleep(40 * time.Millisecond)
				t.mu.Lock()
				t.pend = false
				scr := t.build()
				t.lastRender = time.Now()
				t.mu.Unlock()
				fmt.Print(scr)
			}()
		}
		t.mu.Unlock()
		return
	}
	scr := t.build()
	t.lastRender = time.Now()
	t.mu.Unlock()
	fmt.Print(scr)
}

// build menyusun seluruh layar sebagai satu string (dipanggil sambil
// memegang mutex). Susunan: header 1 baris + isi + menu + status + input.
func (t *TUI) build() string {
	w, h := t.w, t.h
	if w < 40 {
		w = 40
	}
	if h < 8 {
		h = 8
	}
	body := h - 3 // header + status + input
	// Jendela menu slash/picker: scrollable (wheel) + indikator atas/bawah.
	menuAll := t.menu
	if t.menuOff >= len(menuAll) {
		t.menuOff = 0
	}
	visCap := body - 1 // ruang maksimum baris menu (sisanya viewport)
	maxRows := visCap - 2
	if maxRows < 1 {
		maxRows = 1
	}
	winStart := t.menuOff
	winEnd := winStart + maxRows
	if winEnd > len(menuAll) {
		winEnd = len(menuAll)
	}
	hasAbove := winStart > 0
	hasBelow := winEnd < len(menuAll)
	rows := body - (winEnd - winStart)
	if hasAbove {
		rows--
	}
	if hasBelow {
		rows--
	}
	if rows < 1 {
		rows = 1
	}
	t.menuBaseY = rows + 2 // baris layar 1-based dari baris menu pertama
	t.menuWinStart = winStart
	t.menuWinRows = winEnd - winStart

	// Viewport = transkrip (dilipat sesuai lebar kini) + live; digeser oleh
	// scroll dari ekor (scroll=0 ikuti ekor; scroll>0 menahan pandang ke atas).
	cb := t.cur()
	liveLines := wrapANSI(cb.live, w-2)
	if cb.live == "" {
		liveLines = nil
	}
	var all []string
	for _, ln := range cb.lines {
		all = append(all, wrapANSI(ln, w-2)...)
	}
	all = append(all, liveLines...)
	off := cb.scroll
	if off > len(all) {
		off = len(all)
	}
	end := len(all) - off
	start := end - rows
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	var view []string
	if end > start {
		view = append(view, all[start:end]...)
	}
	for len(view) < rows {
		// saat scroll melebihi isi, isi sisa dengan kosong di BAWAH agar
		// konten yang ditahan tetap menempel di atas.
		view = append(view, "")
	}

	var b strings.Builder
	b.WriteString("\x1b[H")
	// Header: strip tab [1][2]… + tombol "+" + garis + tombol × di kanan.
	// Setiap label terekam sebagai zona klik (dipakai handler mouse).
	t.headerZones = t.headerZones[:0]
	col := 0
	var tb strings.Builder
	for i, tab := range t.tabs {
		label := tab.title
		if label == "" {
			label = fmt.Sprintf("TAB %d", i+1)
		}
		if len(t.tabs) > 1 {
			label = " " + label + " "
		}
		if i == t.curIdx {
			tb.WriteString(Bold(Cyan("[" + label + "]")))
		} else {
			tb.WriteString(Dim("[" + label + "]"))
		}
		// zona dihitung dari lebar TERLIHAT ("─ " + label…)
		start := 2 + col
		col += visibleLen("[" + label + "]")
		t.headerZones = append(t.headerZones, hdrZone{start, col + 1, 't', i})
	}
	if len(t.tabs) < 9 {
		tb.WriteString(Dim("[+tab]"))
		start := 2 + col
		col += visibleLen("[+tab]")
		t.headerZones = append(t.headerZones, hdrZone{start, col + 1, 'a', -1})
	}
	// tombol tutup × di sisi kanan (bila >1 tab)
	head := "─ " + tb.String()
	if pad := w - 1 - visibleLen(head); pad > 0 {
		head += strings.Repeat("─", pad)
	}
	if len(t.tabs) > 1 {
		head += Bold(Red("×"))
		t.headerZones = append(t.headerZones, hdrZone{w, w, 'c', -1})
	}
	b.WriteString(head + "\x1b[K\r\n")
	for _, l := range view {
		b.WriteString(l + "\x1b[K\r\n")
	}
	// Menu slash / picker: window [winStart,winEnd) + indikator scroll.
	if hasAbove {
		b.WriteString(" │ " + Dim(fmt.Sprintf("▲ %d pilihan di atas (scroll sentuh)", winStart)) + "\x1b[K\r\n")
	}
	for i := winStart; i < winEnd; i++ {
		m := menuAll[i]
		var row string
		if i == t.sel {
			row = " │ " + Cyan("❯") + " " + Bold(Cyan(m.Cmd)) + " " + Dim(m.Desc)
		} else {
			row = " │   " + m.Cmd + " " + Dim(m.Desc)
		}
		b.WriteString(row + "\x1b[K\r\n")
	}
	if hasBelow {
		b.WriteString(" │ " + Dim(fmt.Sprintf("▼ %d pilihan di bawah (scroll sentuh)", len(menuAll)-winEnd)) + "\x1b[K\r\n")
	}
	// Bar status: indikator scroll + animasi berpikir + status dasar,
	// dipotong di sisi KANAN agar identitas sesi tetap terbaca.
	st := ""
	if cb.scroll > 0 {
		st += fmt.Sprintf(" ▲ %d baris │", cb.scroll)
	}
	if t.thinkOn {
		st += " " + spinnerFrames[t.thinkFrame%len(spinnerFrames)] + " " + t.thinkLabel + " │"
	}
	st += cb.status
	if vis := visibleLen(st); vis > w-1 {
		r := []rune(st)
		st = string(r[:w-1]) // keep-left: potong sisi kanan
	}
	b.WriteString(Dim(st) + "\x1b[K\r\n")
	inp := " " + Bold(Cyan(t.prefix+" ")) + string(t.buf)
	if t.liveInput != "" {
		// Operator sedang mengetik SAAT engine bekerja — tampilkan di bar
		// input: Enter mengirimnya ke antrian model, Esc tidak dipakai.
		inp = " " + Bold(Yellow("⌨ ")) + Bold(t.liveInput) + Dim("  · Enter: kirim ke model")
		if vis := visibleLen(inp); vis > w-1 {
			inp = truncRunesVisible(inp, w-1)
		}
	}
	b.WriteString(inp + "\x1b[K")
	return b.String()
}

// truncRunesVisible memotong string ber-ANSI agar lebar terlihatnya ≤ n.
func truncRunesVisible(s string, n int) string {
	var b strings.Builder
	vis := 0
	i := 0
	r := []rune(s)
	for i < len(r) {
		if r[i] == '\x1b' {
			b.WriteRune(r[i])
			i++
			for i < len(r) && vis < n {
				b.WriteRune(r[i])
				if (r[i] >= 'a' && r[i] <= 'z') || (r[i] >= 'A' && r[i] <= 'Z') {
					i++
					break
				}
				i++
			}
			continue
		}
		if vis >= n {
			break
		}
		b.WriteRune(r[i])
		vis++
		i++
	}
	return b.String() + "…"
}

// ReadInputTUI membaca satu baris di baris terbawah layar penuh: menu saran
// slash dirender oleh TUI di atas bar status. Interaksi sama dengan editor
// kotak input: '/' memunculkan menu, ▲▼ memilih, Tab/→ melengkapi.
func ReadInputTUI(t *TUI, prefix string) (string, error) {
	old, err := makeRaw(int(os.Stdin.Fd()))
	if err != nil {
		// Terminal tidak mendukung mode raw — fallback polos.
		fmt.Print(Prompt(prefix + " "))
		r := bufio.NewReader(os.Stdin)
		line, err := r.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return line, nil
	}
	restore := func() {
		fmt.Print("\x1b[?2004l") // matikan bracketed paste
		_ = tcSet(int(os.Stdin.Fd()), old)
	}
	e := &lineEditor{prefix: prefix}
	sync := func() {
		e.recompute()
		t.SetInput(prefix, e.buf, e.match, e.sel)
	}
	sync()
	fmt.Print("\x1b[?2004h") // aktifkan bracketed paste — paste TIDAK otomatis dieksekusi
	r := bufio.NewReader(os.Stdin)
	for {
		ev := readKey(r)
		switch ev.kind {
		case kPaste:
			// Paste masuk ke buffer — baris baru diringkas jadi spasi.
			// TIDAK ada eksekusi otomatis; operator menekan Enter sendiri.
			e.buf = append(e.buf, []rune(collapsePaste(ev.text))...)
			sync()
		case kEnter:
			if e.menu && len(e.match) > 0 && string(e.buf) != e.match[e.sel].Cmd {
				e.buf = []rune(e.match[e.sel].Cmd)
				sync()
				continue
			}
			if strings.TrimSpace(string(e.buf)) == "" {
				e.buf = nil
				sync()
				continue
			}
			restore()
			t.ScrollTo(0) // kembali ikuti ekor saat jawaban baru mengalir
			t.SetInput(prefix, nil, nil, 0)
			return string(e.buf), nil
		case kBackspace:
			if n := len(e.buf); n > 0 {
				e.buf = e.buf[:n-1]
				sync()
			}
		case kCtrlU:
			e.buf = nil
			sync()
		case kUp:
			if e.menu && len(e.match) > 0 {
				e.sel = (e.sel + len(e.match) - 1) % len(e.match)
				sync()
				continue
			}
			t.Scroll(-1) // tanpa menu: geser pandang transkrip satu baris
		case kDown:
			if e.menu && len(e.match) > 0 {
				e.sel = (e.sel + 1) % len(e.match)
				sync()
				continue
			}
			t.Scroll(1)
		case kPgUp:
			t.Scroll(-(t.h - 4))
		case kPgDn:
			t.Scroll(t.h - 4)
		case kCtrlT:
			t.AddTab()
		case kAltDigit:
			t.SwitchTab(int(ev.r - '1'))
		case kTab, kRight:
			if e.menu && len(e.match) > 0 && string(e.buf) != e.match[e.sel].Cmd {
				e.buf = []rune(e.match[e.sel].Cmd)
				sync()
			}
		case kCtrlB:
			if ctrlBHandler != nil {
				ctrlBHandler()
			}
			sync()
		case kMouse:
			btn := int(ev.r)
			var x, y int
			var rel bool
			fmt.Sscanf(ev.text, "%d;%d;%v", &x, &y, &rel)
			t.mu.Lock()
			zones := append([]hdrZone(nil), t.headerZones...)
			mBaseY, mWinStart, mWinRows := t.menuBaseY, t.menuWinStart, t.menuWinRows
			t.mu.Unlock()
			// wheel: sentuh ke bawah = pandang ke atas (lawatan lama),
			// sentuh ke atas = kembali ke ekor — sesuai gerakan jari.
			switch btn {
			case 64: // wheel up → ekor/ke bawah
				if e.menu {
					t.ScrollMenu(-1)
				} else {
					t.Scroll(3)
				}
			case 65: // wheel down → ke atas (lawatan lama)
				if e.menu {
					t.ScrollMenu(1)
				} else {
					t.Scroll(-3)
				}
			case 35: // hover (gerakan tanpa tombol): pilih baris menu
				if e.menu && len(e.match) > 0 && y >= mBaseY && y < mBaseY+mWinRows {
					t.sel = mWinStart + (y - mBaseY)
					sync()
				}
			case 0, 1, 2: // klik kiri/tengah/kanan
				if y == 1 { // bar header: zona tab / +tab / ×
					for _, z := range zones {
						if x >= z.start && x <= z.end {
							switch z.kind {
							case 't':
								t.SwitchTab(z.tab)
							case 'a':
								t.AddTab()
							case 'c':
								t.CloseTab(t.Current())
							}
							e.buf = nil
							sync()
							return "", nil // setelah kelola tab, minta input baru
						}
					}
				} else if e.menu && len(e.match) > 0 && y >= mBaseY && y < mBaseY+mWinRows && !rel {
					// klik baris menu = langsung pilih & jalankan
					t.sel = mWinStart + (y - mBaseY)
					e.buf = []rune(e.match[t.sel].Cmd)
					sync()
					restore()
					t.ScrollTo(0)
					t.SetInput(prefix, nil, nil, 0)
					return string(e.buf), nil
				}
			}
		case kCtrlD:
			if len(e.buf) == 0 {
				restore()
				return "", io.EOF
			}
		case kCtrlC:
			restore()
			if interruptHandler != nil {
				interruptHandler()
			}
			os.Exit(130)
		case kRune:
			e.buf = append(e.buf, ev.r)
			sync()
		}
	}
}
