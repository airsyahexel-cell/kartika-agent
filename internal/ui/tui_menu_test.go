//go:build linux || darwin

// tui_menu_test.go — uji regresi jendela menu slash & hover header.
package ui

import (
	"regexp"
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

// TestTombolTutupMenempelTabAktif memastikan tombol × berada DI DALAM
// bracket tab yang aktif — ditampilkan sebagai [ sesi ×] — bukan di ujung
// kanan layar dan bukan menggantung di luar bracket.
func TestTombolTutupMenempelTabAktif(t *testing.T) {
	tu := &TUI{w: 60, h: 20, hoverTab: -1,
		tabs: []*tabBuf{{title: "AAAA"}, {title: "BBBB"}}, curIdx: 1}
	scr := tu.build()

	// Header harus memuat pola "[ BBBB ×]" (spasi boleh bervariasi).
	plain := regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`).ReplaceAllString(scr, "")
	if !strings.Contains(plain, "[ BBBB ×]") {
		t.Fatalf("× tidak berada di dalam bracket tab aktif.\nHeader: %q", plain)
	}

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
	// × berada di dalam bracket tab aktif: kolomnya harus di ANTARA awal dan
	// akhir zona tab (zona tab kini mencakup "×]" supaya tidak ada kolom mati).
	if closeZ.start <= tabZ.start || closeZ.end >= tabZ.end {
		t.Fatalf("× tidak berada di dalam bracket tab aktif: tab=%v close=%v", tabZ, closeZ)
	}
	// dan jauh dari ujung kanan layar (perilaku lama).
	if closeZ.start > tu.w-5 {
		t.Fatalf("× masih menempel di ujung kanan layar: start=%d w=%d", closeZ.start, tu.w)
	}
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

// TestMenuIndexAtBarisLayar memastikan pemetaan baris layar → indeks pilihan
// menu konsisten dengan jendela yang sedang tampil. Ini akar bug lama: hover
// memakai perhitungan sendiri dan menyetel t.sel (yang ditimpa sync()),
// sehingga baris menu tidak pernah menyala saat kursor digerakkan.
func TestMenuIndexAtBarisLayar(t *testing.T) {
	var menu []SlashCommand
	for i := 0; i < 20; i++ {
		menu = append(menu, SlashCommand{Cmd: "/cmd" + string(rune('a'+i)), Desc: "x"})
	}
	tu := &TUI{w: 80, h: 16, tabs: []*tabBuf{{title: "s"}}, menu: menu, sel: 12}
	tu.build()

	base, start, rows := tu.menuBaseY, tu.menuWinStart, tu.menuWinRows
	if rows <= 0 || base <= 0 {
		t.Fatalf("geometri menu tidak terisi: base=%d rows=%d", base, rows)
	}
	if tu.sel < start || tu.sel >= start+rows {
		t.Fatalf("pilihan %d di luar jendela [%d,%d)", tu.sel, start, start+rows)
	}

	// Setiap baris jendela harus memetakan ke indeks yang benar.
	for y := base; y < base+rows; y++ {
		idx, ok := tu.menuIndexAt(y)
		if !ok {
			t.Fatalf("baris %d seharusnya baris menu", y)
		}
		if idx != start+(y-base) {
			t.Fatalf("baris %d memetakan ke %d, harusnya %d", y, idx, start+(y-base))
		}
	}
	// Di luar jendela: bukan baris menu.
	for _, y := range []int{1, base - 1, base + rows, base + rows + 5} {
		if _, ok := tu.menuIndexAt(y); ok {
			t.Fatalf("baris %d seharusnya BUKAN baris menu", y)
		}
	}
}
