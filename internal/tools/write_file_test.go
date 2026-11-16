package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteFileSitrepFallback: folder sitrep/ TIDAK ada di direktori kerja
// → laporan otomatis diarahkan ke folder sitrep di HOME user (lintas OS).
func TestWriteFileSitrepFallback(t *testing.T) {
	// Direktori kerja kosong (tanpa sitrep/)
	cwd, _ := os.Getwd()
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	home, _ := os.UserHomeDir()
	homeSitrep := filepath.Join(home, "sitrep")
	// bersihkan sisa uji sebelumnya (hanya file uji spesifik)

	reg := NewRegistry()
	RegisterBuiltins(reg, ".")
	res := reg.Call("write_file", json.RawMessage(`{"path":"sitrep/sitrep-uji-fallback.md","content":"# uji fallback"}`))
	if !strings.Contains(res, "=== FILE DITULIS ===") {
		t.Fatalf("penulisan gagal: %s", res)
	}
	if !strings.Contains(res, homeSitrep) {
		t.Fatalf("fallback HOME tidak dipakai: %s", res)
	}
	data, err := os.ReadFile(filepath.Join(homeSitrep, "sitrep-uji-fallback.md"))
	if err != nil || !strings.Contains(string(data), "uji fallback") {
		t.Fatalf("file laporan tidak ada di HOME/sitrep: %v", err)
	}

	// Sekarang buat folder sitrep/ di cwd → penulisan kembali ke cakupan kerja.
	if err := os.MkdirAll(filepath.Join(tmp, "sitrep"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = homeSitrep
	res2 := reg.Call("write_file", json.RawMessage(`{"path":"sitrep/sitrep-lokal.md","content":"lokal"}`))
	if !strings.Contains(res2, filepath.Join(tmp, "sitrep")) {
		t.Fatalf("cakupan kerja tidak dipakai lagi setelah sitrep/ ada: %s", res2)
	}
}
