//go:build linux || darwin

// readline_common.go — editor baris mode-raw dengan menu saran perintah
// slash (gaya Claude Code): saat operator mengetik "/" di kotak input,
// daftar perintah tersedia muncul live dan bisa dipilih dengan panah.
package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

// interruptHandler dipasang dari main agar Ctrl+C (mode raw) tetap
// membersihkan sumber daya (mis. MCP server) sebelum keluar.
var interruptHandler func()

// SetInterruptHandler memasang handler pembersihan untuk Ctrl+C mode raw.
func SetInterruptHandler(f func()) { interruptHandler = f }

// ctrlBHandler dipasang dari main: menampilkan/menyembunyikan alur berpikir
// model (reasoning) — dipicu Ctrl+B baik saat engine berpikir maupun di
// kotak input.
var ctrlBHandler func()

// SetCtrlBHandler memasang handler toggle pemikiran model untuk Ctrl+B.
func SetCtrlBHandler(f func()) { ctrlBHandler = f }

// SlashCommand dan SlashCommands pindah ke ui.go agar tersedia di semua
// platform (dipakai juga oleh TUI layar penuh).

type keyKind int

const (
	kRune keyKind = iota
	kEnter
	kBackspace
	kUp
	kDown
	kRight
	kTab
	kCtrlC
	kCtrlD
	kCtrlU
	kCtrlB
	kCtrlT
	kPgUp
	kPgDn
	kAltDigit
	kMouse
	kPaste
	kOther
)

type keyEvent struct {
	kind keyKind
	r    rune
	text string // isi paste / data mouse "btn;x;y;rel" (rel: true = lepas)
}

// filterSlash menyaring daftar perintah sesuai awalan yang diketik.
func filterSlash(buf []rune) []SlashCommand {
	p := strings.ToLower(string(buf))
	var out []SlashCommand
	for _, c := range SlashCommands {
		cmdLow := strings.ToLower(c.Cmd)
		if strings.HasPrefix(cmdLow, p) || strings.HasPrefix(strings.ReplaceAll(cmdLow, " ", "-"), p) {
			out = append(out, c)
		}
	}
	return out
}

// lineEditor menyimpan status satu sesi ketik (buffer + menu saran).
type lineEditor struct {
	prefix   string
	buf      []rune
	menu     bool
	sel      int
	match    []SlashCommand
	matchFor string // teks buffer saat daftar saran terakhir dihitung
}

// recompute memperbarui daftar saran & visibilitas menu setelah buffer berubah.
// Pilihan (sel) hanya direset bila teks buffer berubah — sehingga panah ↑↓
// yang hanya menggeser sorotan tidak ikut dibatalkan oleh render ulang.
func (e *lineEditor) recompute() {
	s := string(e.buf)
	teksBerubah := s != e.matchFor
	e.matchFor = s
	e.match = nil
	if teksBerubah {
		e.sel = 0
	}
	if strings.HasPrefix(s, "/") {
		e.match = filterSlash(e.buf)
	}
	e.menu = len(e.match) > 0
	if e.sel >= len(e.match) {
		e.sel = 0
	}
	if e.sel < 0 {
		e.sel = 0
	}
}

// cursorCol mengembalikan posisi kolom kursor di akhir buffer pada baris input.
func (e *lineEditor) cursorCol() int {
	return 2 + len([]rune(e.prefix+" ")) + len(e.buf)
}

// draw menggambar ulang baris input (+ menu bila terbuka). Kursor dianggap
// berada di akhir buffer pada baris input saat fungsi ini dipanggil.
func (e *lineEditor) draw() {
	fmt.Print("\r\x1b[J") // bersihkan menu lama + baris input lama
	fmt.Print("│ " + Bold(Cyan(e.prefix+" ")) + string(e.buf))
	if e.menu {
		fmt.Println()
		for i, m := range e.match {
			if i == e.sel {
				fmt.Printf("│ %s %s %s\n", Cyan("❯"), Bold(Cyan(m.Cmd)), Dim(m.Desc))
			} else {
				fmt.Printf("│   %s %s\n", m.Cmd, Dim(m.Desc))
			}
		}
		// Setelah baris menu terakhir dicetak dengan '\n', kursor berada
		// satu baris di bawahnya — naik len(menu)+1 agar kembali ke baris input.
		fmt.Printf("\x1b[%dA\r\x1b[%dC", len(e.match)+1, e.cursorCol())
	}
}

// readKey membaca satu keystroke dari stdin (mendukung multi-byte UTF-8
// dan urutan tombol panah).
func readKey(r *bufio.Reader) keyEvent {
	b, err := r.ReadByte()
	if err != nil {
		return keyEvent{kind: kCtrlD} // EOF → perlakukan sebagai keluar
	}
	switch b {
	case '\r', '\n':
		return keyEvent{kind: kEnter}
	case 0x7f, 0x08:
		return keyEvent{kind: kBackspace}
	case '\t':
		return keyEvent{kind: kTab}
	case 0x03:
		return keyEvent{kind: kCtrlC}
	case 0x04:
		return keyEvent{kind: kCtrlD}
	case 0x15:
		return keyEvent{kind: kCtrlU}
	case 0x02:
		return keyEvent{kind: kCtrlB}
	case 0x14:
		return keyEvent{kind: kCtrlT}
	case 0x1b: // urutan escape: ESC [ A|B|C|D ... atau Alt+tombol
		c1, err := r.ReadByte()
		if err != nil {
			return keyEvent{kind: kOther}
		}
		if c1 != '[' {
			// Alt+<tombol>: alt+1..9 dipakai untuk pindah tab.
			if c1 >= '1' && c1 <= '9' {
				return keyEvent{kind: kAltDigit, r: rune(c1)}
			}
			return keyEvent{kind: kOther}
		}
		c2, err := r.ReadByte()
		if err != nil {
			return keyEvent{kind: kOther}
		}
		switch c2 {
		case '<': // mouse SGR: ESC [ < btn ; x ; y M|m
			var sb []byte
			for {
				c3, err2 := r.ReadByte()
				if err2 != nil {
					return keyEvent{kind: kOther}
				}
				sb = append(sb, c3)
				if c3 == 'M' || c3 == 'm' {
					break
				}
				if len(sb) > 32 {
					return keyEvent{kind: kOther}
				}
			}
			rel := sb[len(sb)-1] == 'm'
			body := string(sb[:len(sb)-1])
			parts := strings.SplitN(body, ";", 3)
			if len(parts) != 3 {
				return keyEvent{kind: kOther}
			}
			btn, e1 := strconv.Atoi(parts[0])
			x, e2 := strconv.Atoi(parts[1])
			y, e3 := strconv.Atoi(parts[2])
			if e1 != nil || e2 != nil || e3 != nil {
				return keyEvent{kind: kOther}
			}
			return keyEvent{kind: kMouse, r: rune(btn), text: fmt.Sprintf("%d;%d;%v", x, y, rel)}
		case 'A':
			return keyEvent{kind: kUp}
		case 'B':
			return keyEvent{kind: kDown}
		case 'C':
			return keyEvent{kind: kRight}
		case '2': // bracketed paste: ESC[200~ teks ESC[201~
			c3, err := r.ReadByte()
			if err != nil {
				return keyEvent{kind: kOther}
			}
			if c3 == '0' {
				c4, err := r.ReadByte()
				if err != nil {
					return keyEvent{kind: kOther}
				}
				if c5, err := r.ReadByte(); err == nil && c4 == '0' && c5 == '~' {
					// PASTE: kumpulkan seluruh isi sampai penanda akhir.
					var txt []byte
					for {
						pb, perr := r.ReadByte()
						if perr != nil {
							break
						}
						if pb == 0x1b {
							m1, e1 := r.ReadByte()
							if e1 != nil {
								break
							}
							if m1 == '[' {
								c2b, e2 := r.ReadByte()
								c3b, e3 := r.ReadByte()
								c4b, e4 := r.ReadByte()
								c5b, e5 := r.ReadByte()
								if e2 != nil || e3 != nil || e4 != nil || e5 != nil {
									break
								}
								if c2b == '2' && c3b == '0' && c4b == '1' && c5b == '~' {
									break // penanda akhir paste
								}
								txt = append(txt, 0x1b, m1, c2b, c3b, c4b, c5b)
								continue
							}
							txt = append(txt, 0x1b, m1)
							continue
						}
						txt = append(txt, pb)
					}
					return keyEvent{kind: kPaste, text: string(txt)}
				}
			}
			return keyEvent{kind: kOther}
		case '3': // Delete = ESC [ 3 ~
			_, _ = r.ReadByte()
			return keyEvent{kind: kOther}
		case '5': // PgUp = ESC [ 5 ~
			if c3, err := r.ReadByte(); err == nil && c3 == '~' {
				return keyEvent{kind: kPgUp}
			}
			return keyEvent{kind: kOther}
		case '6': // PgDn = ESC [ 6 ~
			if c3, err := r.ReadByte(); err == nil && c3 == '~' {
				return keyEvent{kind: kPgDn}
			}
			return keyEvent{kind: kOther}
		}
		return keyEvent{kind: kOther}
	}
	if b < 0x80 {
		return keyEvent{kind: kRune, r: rune(b)}
	}
	// Multi-byte UTF-8.
	nb := 1
	switch {
	case b&0xE0 == 0xC0:
		nb = 2
	case b&0xF0 == 0xE0:
		nb = 3
	case b&0xF8 == 0xF0:
		nb = 4
	}
	p := make([]byte, 0, nb)
	p = append(p, b)
	for len(p) < nb {
		c, err := r.ReadByte()
		if err != nil {
			return keyEvent{kind: kOther}
		}
		p = append(p, c)
	}
	rn, _ := utf8.DecodeRune(p)
	return keyEvent{kind: kRune, r: rn}
}

var pipeStdinReader = bufio.NewReader(os.Stdin)

// ReadInput membaca satu baris di kotak input mode posko. Pada terminal
// interaktif (Linux/macOS) memakai mode raw sehingga mengetik "/" langsung
// memunculkan menu saran perintah; pada pipe/CI jatuh ke input polos.
func ReadInput(prefix string) (string, error) {
	if !useColor || !IsTTY() {
		fmt.Print(Prompt(prefix + " "))
		line, err := pipeStdinReader.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	return readRawLine(prefix)
}

func readRawLine(prefix string) (string, error) {
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
	w := termWidth()
	bottom := Cyan("╰" + strings.Repeat("─", w-2) + "╯")

	// closeBox menutup kotak input tanpa menghapus teks yang diketik:
	// menu di bawahnya dihapus, teks input tetap terlihat di scrollback.
	closeBox := func() {
		if e.menu {
			fmt.Printf("\x1b[1B\x1b[J\x1b[1A\r\x1b[%dC", e.cursorCol())
		}
		fmt.Println()
		fmt.Println(bottom)
	}

	fmt.Println(Cyan("╭" + strings.Repeat("─", w-2) + "╮"))
	fmt.Print("\x1b[?2004h") // aktifkan bracketed paste — paste TIDAK otomatis dieksekusi
	e.recompute()
	e.draw()

	r := bufio.NewReader(os.Stdin)
	for {
		ev := readKey(r)
		switch ev.kind {
		case kPaste:
			// Paste masuk ke buffer — baris baru diringkas jadi spasi.
			// TIDAK ada eksekusi otomatis; operator menekan Enter sendiri.
			e.buf = append(e.buf, []rune(collapsePaste(ev.text))...)
			e.recompute()
			e.draw()
		case kEnter:
			// Masih sebagian perintah → lengkapi dulu dari menu saran.
			if e.menu && len(e.match) > 0 && string(e.buf) != e.match[e.sel].Cmd {
				e.buf = []rune(e.match[e.sel].Cmd)
				e.recompute()
				e.draw()
				continue
			}
			// Enter pada baris kosong: tetap di kotak yang sama —
			// jangan tutup lalu buka ulang kotak (kesan "dobel").
			if strings.TrimSpace(string(e.buf)) == "" {
				e.buf = nil
				e.recompute()
				e.draw()
				continue
			}
			restore()
			closeBox()
			return string(e.buf), nil
		case kBackspace:
			if n := len(e.buf); n > 0 {
				e.buf = e.buf[:n-1]
				e.recompute()
				e.draw()
			}
		case kCtrlU:
			e.buf = nil
			e.recompute()
			e.draw()
		case kCtrlB:
			if ctrlBHandler != nil {
				ctrlBHandler()
			}
			e.recompute()
			e.draw()
		case kUp:
			if e.menu && len(e.match) > 0 {
				e.sel = (e.sel + len(e.match) - 1) % len(e.match)
				e.draw()
			}
		case kDown:
			if e.menu && len(e.match) > 0 {
				e.sel = (e.sel + 1) % len(e.match)
				e.draw()
			}
		case kTab, kRight:
			if e.menu && len(e.match) > 0 && string(e.buf) != e.match[e.sel].Cmd {
				e.buf = []rune(e.match[e.sel].Cmd)
				e.recompute()
				e.draw()
			}
		case kRune:
			e.buf = append(e.buf, ev.r)
			e.recompute()
			e.draw()
		case kCtrlD:
			if len(e.buf) == 0 {
				restore()
				closeBox()
				return "", io.EOF
			}
		case kCtrlC:
			if len(e.buf) == 0 {
				restore()
				closeBox()
				if interruptHandler != nil {
					interruptHandler()
				}
				os.Exit(130)
			}
			e.buf = nil
			e.recompute()
			e.draw()
		}
	}
}
