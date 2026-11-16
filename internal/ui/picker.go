//go:build linux || darwin

// picker.go — picker interaktif layar penuh: anak panah ↑↓ berwarna,
// hover mouse memilih, klik langsung memilih, wheel menggulir daftar,
// Enter = pilih, Esc/q = batal. Dipakai /model, /api-url, dan wizard
// konfigurasi lainnya di mode TUI; mode CLI polos memakai fallback
// daftar bernomor (lihat main.go).
package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// PickerItem didefinisikan di picker_types.go agar tersedia di semua platform.

// ReadPickerTUI menampilkan picker di atas layar alternatif aktif.
// Mengembalikan (index, true) bila dipilih, (-1, false) bila batal.
// Panah atas/bawah berpindah (pilihan di-highlight), mouse hover
// memilih, klik langsung memilih, wheel menggulir jendela daftar.
func ReadPickerTUI(title string, items []PickerItem) (int, bool) {
	if !TUIAvailable() || len(items) == 0 {
		return -1, false
	}
	old, err := makeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return -1, false
	}
	defer func() {
		DisableMouse()
		_ = tcSet(int(os.Stdin.Fd()), old)
	}()
	EnableMouse()

	sel, winStart := 0, 0
	draw := func() {
		w, h := termSize()
		if h < 8 {
			h = 8
		}
		if w < 40 {
			w = 40
		}
		listH := h - 6 // title + garis + status + margin
		if listH < 1 {
			listH = 1
		}
		// pastikan pilihan terlihat di jendela
		if sel < winStart {
			winStart = sel
		}
		if sel >= winStart+listH {
			winStart = sel - listH + 1
		}
		if winStart > len(items)-1 {
			winStart = len(items) - 1
		}
		if winStart < 0 {
			winStart = 0
		}
		var b strings.Builder
		b.WriteString("\x1b[H\x1b[2J")
		b.WriteString(Cyan("╭─ "+Bold(title)+" "+strings.Repeat("─", maxInt(0, w-6-visibleLen(title)))+"╮") + "\r\n")
		end := winStart + listH
		if end > len(items) {
			end = len(items)
		}
		for i := winStart; i < end; i++ {
			row := " │ "
			mark := "  "
			if i == sel {
				mark = Cyan("❯ ")
			}
			row += mark
			if i == sel {
				row += Bold(Cyan(items[i].Label))
			} else {
				row += items[i].Label
			}
			if items[i].Desc != "" {
				row += "  " + Dim(items[i].Desc)
			}
			b.WriteString(row + "\x1b[K")
			b.WriteString("\r\n")
		}
		// kaki: status + hint
		for i := end - winStart; i < listH; i++ {
			b.WriteString(" │\x1b[K\r\n")
		}
		info := fmt.Sprintf(" │ %d/%d", sel+1, len(items))
		if winStart > 0 {
			info += Dim("  ▲")
		}
		if end < len(items) {
			info += Dim("  ▼")
		}
		b.WriteString(info + "\x1b[K\r\n")
		b.WriteString(Cyan("╰"+strings.Repeat("─", w-2)+"╯") + "\r\n")
		b.WriteString(Dim("  ↑↓ pilih · Enter ya · Esc batal · mouse: hover/klik/wheel") + "\x1b[K")
		fmt.Print(b.String())
	}

	// geometri daftar untuk klik mouse: baris pertama daftar = baris 2.
	listY0, listH := 2, func() int {
		_, h := termSize()
		lh := h - 6
		if lh < 1 {
			lh = 1
		}
		return lh
	}()

	draw()
	r := bufio.NewReader(os.Stdin)
	for {
		ev := readKey(r)
		switch ev.kind {
		case kUp:
			if sel > 0 {
				sel--
			}
			draw()
		case kDown:
			if sel < len(items)-1 {
				sel++
			}
			draw()
		case kEnter:
			fmt.Print("\x1b[H\x1b[2J")
			return sel, true
		case kCtrlC, kCtrlD:
			fmt.Print("\x1b[H\x1b[2J")
			return -1, false
		case kOther:
			// Esc biasanya datang sebagai byte 0x1b tunggal
			return -1, false
		case kMouse:
			btn := int(ev.r)
			var x, y int
			var rel bool
			fmt.Sscanf(ev.text, "%d;%d;%v", &x, &y, &rel)
			switch btn {
			case 64, 65:
				if btn == 64 && winStart > 0 {
					winStart--
				} else if btn == 65 && winStart+listH < len(items) {
					winStart++
				}
				draw()
			case 35: // hover — pilih
				if y >= listY0 && y < listY0+listH {
					i := winStart + (y - listY0)
					if i >= 0 && i < len(items) && i != sel {
						sel = i
						draw()
					}
				}
			case 0, 1, 2: // klik (proses saat tekan)
				if !rel && y >= listY0 && y < listY0+listH {
					i := winStart + (y - listY0)
					if i >= 0 && i < len(items) {
						fmt.Print("\x1b[H\x1b[2J")
						return i, true
					}
				}
			}
		case kRune:
			if ev.r == 'q' || ev.r == 'Q' {
				fmt.Print("\x1b[H\x1b[2J")
				return -1, false
			}
			if ev.r == 'j' {
				if sel < len(items)-1 {
					sel++
					draw()
				}
			} else if ev.r == 'k' {
				if sel > 0 {
					sel--
					draw()
				}
			}
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
