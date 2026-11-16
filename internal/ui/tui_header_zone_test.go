//go:build linux || darwin

package ui

import (
	"strings"
	"testing"
)

// TestZonaHeaderSetiapKolom memastikan tidak ada kolom "mati" di dalam
// bracket tab: setiap kolom milik tab harus menyorot tab itu, dan kolom ×
// harus menyorot tombol tutup.
func TestZonaHeaderSetiapKolom(t *testing.T) {
	tu := &TUI{w: 90, h: 20, hoverTab: -1, tabs: []*tabBuf{{title: "A"}}}
	tu.AddTab()
	tu.AddTab()
	tu.build()

	var tabZones []hdrZone
	var closeZ hdrZone
	for _, z := range tu.headerZones {
		switch z.kind {
		case 't':
			tabZones = append(tabZones, z)
		case 'c':
			closeZ = z
		}
	}
	if len(tabZones) != 3 {
		t.Fatalf("harus ada 3 zona tab, dapat %d", len(tabZones))
	}
	for _, z := range tabZones {
		for x := z.start; x <= z.end; x++ {
			tu.setHoverFromHeader(x, tu.headerZones)
			if tu.hoverTab != z.tab {
				t.Fatalf("x=%d harus menyorot tab %d, dapat %d (zona %v)", x, z.tab, tu.hoverTab, z)
			}
		}
	}
	if closeZ.end == 0 {
		t.Fatal("zona × tidak ditemukan")
	}
	tu.setHoverFromHeader(closeZ.start, tu.headerZones)
	if !tu.hoverClose {
		t.Fatalf("x=%d harus menyorot tombol ×", closeZ.start)
	}
}

// TestTabAktifDihoverBergarisBawah memastikan tab aktif tetap memberi umpan
// balik saat kursor melintas di atasnya (bukan diam seperti sebelumnya).
func TestTabAktifDihoverBergarisBawah(t *testing.T) {
	tu := &TUI{w: 80, h: 20, hoverTab: -1, tabs: []*tabBuf{{title: "AAAA"}, {title: "BBBB"}}, curIdx: 1}
	tu.build()
	var tabZ hdrZone
	for _, z := range tu.headerZones {
		if z.kind == 't' && z.tab == 1 {
			tabZ = z
		}
	}
	if tabZ.end == 0 {
		t.Fatal("zona tab aktif tidak ditemukan")
	}
	tu.SetHover(tabZ.tab, false, false)
	scr := tu.build()
	if !strings.Contains(scr, "\x1b[4m") {
		t.Fatal("tab aktif yang di-hover tidak menampilkan garis bawah")
	}
}
