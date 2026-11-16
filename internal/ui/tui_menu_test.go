//go:build linux || darwin

// tui_menu_test.go — uji regresi jendela menu slash & hover header.
package ui

import (
	"strings"
	"testing"
)

// TestJendelaMenuIkutiPilihan memastikan panah ↓ tidak membuat pilihan
// aktif menghilang dari layar pada terminal pendek. Sebelum perbaikan,
// menuOff hanya berubah lewat wheel sehingga sorotan "keluar" jendela.
func TestJendelaMenuIkutiPilihan(t *testing.T) {
	var menu []SlashCommand
	for i := 0; i < 20; i++ {
		menu = append(menu, SlashCommand{Cmd: "/cmd" + string(rune('a'+i)), Desc: "x"})
	}
	tu := &TUI{w: 80, h: 12, tabs: []*tabBuf{{title: "s"}}, menu: menu, sel: 0}

	tu.build()
	awal := tu.menuWinStart
	if awal != 0 {
		t.Fatalf("jendela awal harus 0, dapat %d", awal)
	}

	// Sorotan turun ke pilihan terakhir → jendela wajib mengikuti.
	tu.sel = len(menu) - 1
	tu.build()
	if tu.menuWinStart == awal {
		t.Fatalf("jendela tidak mengikuti sorotan (tetap %d)", tu.menuWinStart)
	}
	if tu.sel < tu.menuWinStart || tu.sel >= tu.menuWinStart+tu.menuWinRows {
		t.Fatalf("sorotan %d di luar jendela [%d,%d)",
			tu.sel, tu.menuWinStart, tu.menuWinStart+tu.menuWinRows)
	}

	// Sorotan kembali ke atas → jendela ikut naik.
	tu.sel = 0
	tu.build()
	if tu.menuWinStart != 0 {
		t.Fatalf("jendela harus kembali ke 0, dapat %d", tu.menuWinStart)
	}
}

// TestJendelaMenuGeserManual memastikan wheel tetap bisa menggeser jendela
// tanpa dilawan oleh perilaku "ikuti pilihan".
func TestJendelaMenuGeserManual(t *testing.T) {
	var menu []SlashCommand
	for i := 0; i < 20; i++ {
		menu = append(menu, SlashCommand{Cmd: "/cmd" + string(rune('a'+i))})
	}
	tu := &TUI{w: 80, h: 12, tabs: []*tabBuf{{title: "s"}}, menu: menu, sel: 5}

	for i := 0; i < 4; i++ {
		tu.ScrollMenu(1)
	}
	geser := tu.menuOff
	if geser <= 0 {
		t.Fatalf("wheel tidak menggeser jendela: menuOff=%d", tu.menuOff)
	}
	tu.build()
	if tu.menuWinStart != geser {
		t.Fatalf("geseran manual dilawan: mau %d, dapat %d", geser, tu.menuWinStart)
	}

	// Menu baru → kembali ke perilaku mengikuti pilihan.
	tu.SetInput("CHAT ❯", []rune("/c"), menu[:3], 0)
	if tu.menuOff != 0 {
		t.Fatalf("menu baru harus mengatur ulang jendela, dapat %d", tu.menuOff)
	}
}

// TestTombolTutupMenempelTabAktif memastikan tombol × berada tepat di
// samping label tab yang aktif (bukan di ujung kanan layar).
func TestTombolTutupMenempelTabAktif(t *testing.T) {
	tu := &TUI{w: 60, h: 20, hoverTab: -1,
		tabs: []*tabBuf{{title: "AAAA"}, {title: "BBBB"}}, curIdx: 1}
	scr := tu.build()

	var tabZ, closeZ hdrZone
	for _, z := range tu.headerZones {
		switch z.kind {
		case 't':
			if z.tab == 1 {
				tabZ = z
			}
		case 'c':
			closeZ = z
		}
	}
	if closeZ.end == 0 {
		t.Fatal("zona tombol tutup × tidak ditemukan")
	}
	// × harus mulai tepat setelah tab aktif berakhir (maksimal 1 kolom jeda).
	if closeZ.start < tabZ.end || closeZ.start > tabZ.end+2 {
		t.Fatalf("× tidak menempel pada tab aktif: tab=%v close=%v", tabZ, closeZ)
	}
	// dan jauh dari ujung kanan layar (perilaku lama).
	if closeZ.start > tu.w-5 {
		t.Fatalf("× masih menempel di ujung kanan layar: start=%d w=%d", closeZ.start, tu.w)
	}
	_ = strings.Contains(scr, "×")
}

// TestHoverTabHeader memastikan hover mouse di bar header menyorot tab
// (efek "hidup" seperti pada picker /pilih model).
func TestHoverTabHeader(t *testing.T) {
	tu := &TUI{w: 80, h: 20, hoverTab: -1,
		tabs: []*tabBuf{{title: "AAAA"}, {title: "BBBB"}}, curIdx: 1}
	tu.build()

	var tab0 hdrZone
	for _, z := range tu.headerZones {
		if z.kind == 't' && z.tab == 0 {
			tab0 = z
		}
	}
	if tab0.end == 0 {
		t.Fatal("zona tab 0 tidak ditemukan")
	}
	tu.setHoverFromHeader((tab0.start+tab0.end)/2, tu.headerZones)
	if tu.hoverTab != 0 {
		t.Fatalf("hover tab 0 tidak terdeteksi, hoverTab=%d", tu.hoverTab)
	}

	// Render tab yang disorot harus memakai atribut video-terbalik.
	scr := tu.build()
	if !strings.Contains(scr, "\x1b[7m") {
		t.Fatal("tab tersorot tidak memakai atribut Sel (reverse video)")
	}

	// Di luar header → sorotan dilepas.
	tu.SetHover(-1, false, false)
	if tu.hoverTab != -1 {
		t.Fatalf("sorotan tidak dilepas, hoverTab=%d", tu.hoverTab)
	}
}
