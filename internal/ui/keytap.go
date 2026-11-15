//go:build linux || darwin

// keytap.go — penangkap tombol selama engine bekerja: operator TETAP bisa
// mengetik pesan ke model (mengantri sampai batas langkah berikutnya),
// meng-paste teks tanpa menjalankan apa pun, men-toggle pemikiran (Ctrl+B),
// dan menghentikan giliran chat dengan Ctrl+C — TANPA keluar dari program.
//
// Terminal dipindah ke mode setengah-raw (echo mati, canonical mati, ISIG
// mati — Ctrl+C dibaca sebagai byte 0x03, bukan SIGINT) dengan polling
// VMIN=0/VTIME=1 sehingga goroutine berhenti rapi dalam ≤100 ms.
package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// KeyTap menangkap tombol selama satu giliran chat berlangsung.
type KeyTap struct {
	stop chan struct{}
	done chan struct{}
}

// NewKeyTap mulai menangkap tombol selagi agen bekerja:
//   - ketikan biasa → buffer input-live (render via liveRender);
//   - Enter        → kirim buffer via liveSubmit (antri ke model);
//   - paste        → bracketed paste: seluruh teks masuk buffer TANPA
//     menjalankan apa pun (Enter tetap ditekan manual);
//   - Ctrl+B       → toggle pemikiran model;
//   - Ctrl+U       → bersihkan buffer;
//   - Ctrl+C       → hentikan giliran chat (liveAbort), BUKAN keluar program.
func NewKeyTap() *KeyTap {
	k := &KeyTap{stop: make(chan struct{}), done: make(chan struct{})}
	if !IsTTY() {
		close(k.done)
		return k
	}
	old, err := makeStreamRaw(int(os.Stdin.Fd()))
	if err != nil {
		close(k.done)
		return k
	}
	// aktifkan bracketed paste: terminal membungkus paste dalam ESC[200~ … ESC[201~
	fmt.Print("\x1b[?2004h")
	go func() {
		defer close(k.done)
		defer func() {
			fmt.Print("\x1b[?2004l")
			_ = tcSet(int(os.Stdin.Fd()), old)
		}()
		r := bufio.NewReader(os.Stdin)
		var buf []rune
		render := func() {
			if liveRender != nil {
				liveRender(string(buf))
			}
		}
		submit := func() {
			text := strings.TrimSpace(string(buf))
			buf = nil
			render()
			if text != "" && liveSubmit != nil {
				liveSubmit(text)
			}
		}
		for {
			select {
			case <-k.stop:
				return
			default:
			}
			b, err := r.ReadByte()
			if err != nil {
				// Timeout VTIME (tanpa data) atau stdin tertutup — ulang
				// sampai Stop() dipanggil.
				select {
				case <-k.stop:
					return
				default:
					continue
				}
			}
			switch {
			case b == 0x03: // Ctrl+C — hentikan giliran chat, bukan program
				if liveAbort != nil {
					liveAbort()
				}
			case b == 0x02: // Ctrl+B — toggle pemikiran model
				if ctrlBHandler != nil {
					ctrlBHandler()
				}
			case b == 0x7f || b == 0x08: // backspace
				if n := len(buf); n > 0 {
					buf = buf[:n-1]
					render()
				}
			case b == 0x15: // Ctrl+U — bersihkan buffer
				buf = nil
				render()
			case b == '\r' || b == '\n': // Enter — kirim ke antrian model
				submit()
			case b == 0x1b: // urutan escape: paste atau tombol khusus
				seq, isPaste, ok := readEscapeSeq(r)
				if !ok {
					continue
				}
				if isPaste {
					buf = append(buf, []rune(collapsePaste(seq))...)
					render()
				}
				// escape lain selama streaming diabaikan
			case b == 0x09: // Tab diabaikan saat engine bekerja
				// (penggantian tab hanya di prompt)
			default:
				if b < 0x80 {
					if b >= 0x20 { // abaikan control char lain
						buf = append(buf, rune(b))
						render()
					}
					continue
				}
				// multi-byte UTF-8
				nb := 1
				switch {
				case b&0xE0 == 0xC0:
					nb = 2
				case b&0xF0 == 0xE0:
					nb = 3
				case b&0xF8 == 0xF0:
					nb = 4
				}
				p := []byte{b}
				for len(p) < nb {
					c, err := r.ReadByte()
					if err != nil {
						break
					}
					p = append(p, c)
				}
				if rn, sz := utf8.DecodeRune(p); sz > 0 && rn != utf8.RuneError {
					buf = append(buf, rn)
					render()
				}
			}
		}
	}()
	return k
}

// readEscapeSeq membaca satu urutan escape setelah byte 0x1b.
// Mengembalikan isi teks paste bila urutan adalah bracketed paste
// (ESC [ 200 ~ … ESC [ 201 ~). ok=false bila stdin tertutup.
func readEscapeSeq(r *bufio.Reader) (seq string, isPaste, ok bool) {
	b, err := r.ReadByte()
	if err != nil {
		return "", false, false
	}
	if b != '[' {
		// ESC O ... atau alt+tombol: buang satu byte parameternya (jika ada)
		return "", false, true
	}
	// baca hingga '~' atau huruf pengakhir
	var sb []byte
	for {
		c, err := r.ReadByte()
		if err != nil {
			return "", false, false
		}
		sb = append(sb, c)
		if c == '~' || (c >= 'A' && c <= 'Z') || c == 'b' {
			break
		}
		if len(sb) > 32 { // pengaman
			break
		}
	}
	s := string(sb)
	if strings.HasPrefix(s, "200~") {
		// PASTE DIMULAI: kumpulkan sampai penanda akhir ESC[201~
		var txt []byte
		for {
			c, err := r.ReadByte()
			if err != nil {
				break
			}
			if c == 0x1b {
				// mungkin penanda akhir: [ 2 0 1 ~
				m1, err := r.ReadByte()
				if err != nil {
					break
				}
				if m1 == '[' {
					c2, err := r.ReadByte()
					if err != nil {
						break
					}
					c3, err := r.ReadByte()
					if err != nil {
						break
					}
					c4, err := r.ReadByte()
					if err != nil {
						break
					}
					c5, err := r.ReadByte()
					if err != nil {
						break
					}
					if c2 == '2' && c3 == '0' && c4 == '1' && c5 == '~' {
						break
					}
					// bukan penanda akhir — pertahankan byte
					txt = append(txt, 0x1b, m1, c2, c3, c4, c5)
					continue
				}
				txt = append(txt, 0x1b, m1)
				continue
			}
			txt = append(txt, c)
		}
		return string(txt), true, true
	}
	return "", false, true
}

// collapsePaste menormalkan teks paste: baris baru dan tab disederhanakan
// menjadi spasi agar pas di editor baris tunggal, whitespace ganda diringkas.
func collapsePaste(s string) string {
	var b strings.Builder
	lastSpace := false
	for _, rn := range s {
		if rn == '\r' || rn == '\n' || rn == '\t' {
			if !lastSpace {
				b.WriteRune(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(rn)
		lastSpace = false
	}
	return b.String()
}

// Stop mengakhiri penangkapan tombol dan mengembalikan mode terminal
// semula (maksimal menunggu ≤100 ms karena polling VTIME).
func (k *KeyTap) Stop() {
	close(k.stop)
	<-k.done
}
