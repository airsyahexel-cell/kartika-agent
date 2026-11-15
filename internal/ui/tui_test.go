//go:build linux || darwin

// tui_test.go — uji lipat baris & viewport TUI layar penuh.
package ui

import (
	"strings"
	"testing"
)

func TestWrapWordsTidakMemotongKata(t *testing.T) {
	s := "Stored XSS di komentar: payload tersimpan dan muncul di halaman"
	got := wrapWords(s, 20)
	for _, l := range got {
		if len([]rune(l)) > 20 {
			t.Fatalf("baris melebihi lebar: %q", l)
		}
	}
	rejoin := strings.Join(got, " ")
	if rejoin != s {
		t.Fatalf("isi berubah:\n asli : %s\n rejoin: %s", s, rejoin)
	}
	for _, kata := range []string{"tersimpan", "muncul", "halaman"} {
		if !strings.Contains(rejoin, kata) {
			t.Fatalf("kata %q hilang karena terpotong keras", kata)
		}
	}
}

func TestWrapANSIKataTidakPutus(t *testing.T) {
	// kata panjang melebihi lebar → dipotong keras, kata normal utuh.
	got := wrapANSI("dua kata dan muncul di halaman", 8)
	rejoin := strings.Join(got, " ")
	if rejoin != "dua kata dan muncul di halaman" {
		t.Fatalf("isi berubah: %q", rejoin)
	}
}

func TestWrapHardANSISelamat(t *testing.T) {
	in := "a\x1b[31m" + strings.Repeat("b", 25) + "\x1b[0m"
	got := wrapHard(in, 10)
	if len(got) < 3 {
		t.Fatalf("harus terlipat jadi >=3 baris, dapat %d", len(got))
	}
	// total 'b' harus tetap 25
	n := 0
	for _, l := range got {
		n += strings.Count(l, "b")
	}
	if n != 25 {
		t.Fatalf("huruf hilang saat lipat keras: %d/25", n)
	}
}

func TestSlashCommandsMode(t *testing.T) {
	// Mode lokal: /pilih model harus di urutan teratas
	SetLocalMode(true)
	matches := filterSlash([]rune("/"))
	if len(matches) == 0 || matches[0].Cmd != "/pilih model" {
		t.Errorf("di mode lokal, /pilih model harus berada di posisi teratas")
	}

	// Filter /pilih atau /pilih-model harus mencocokkan /pilih model
	pilihMatches := filterSlash([]rune("/pilih"))
	if len(pilihMatches) == 0 || pilihMatches[0].Cmd != "/pilih model" {
		t.Errorf("filterSlash(/pilih) harus mencocokkan /pilih model")
	}
	hyphenMatches := filterSlash([]rune("/pilih-m"))
	if len(hyphenMatches) == 0 || hyphenMatches[0].Cmd != "/pilih model" {
		t.Errorf("filterSlash(/pilih-m) harus mencocokkan /pilih model")
	}

	// Mode cloud/API: /konfigurasi-api-key harus di urutan teratas
	SetLocalMode(false)
	matchesCloud := filterSlash([]rune("/"))
	if len(matchesCloud) == 0 || matchesCloud[0].Cmd != "/konfigurasi-api-key" {
		t.Errorf("di mode cloud, /konfigurasi-api-key harus berada di posisi teratas")
	}

	// /konfigurasi-api-key harus selalu ada
	foundKonfig := false
	for _, m := range matchesCloud {
		if m.Cmd == "/konfigurasi-api-key" {
			foundKonfig = true
			break
		}
	}
	if !foundKonfig {
		t.Errorf("perintah /konfigurasi-api-key harus tersedia di menu")
	}
}
