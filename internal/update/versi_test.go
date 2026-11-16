package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestVersiSumberIkutTagTerakhir mencegah jebakan yang pernah terjadi:
// `version` di main.go tertinggal dari tag rilis, sehingga binary hasil
// `go build` polos melaporkan versi lama dan /update justru menawarkan
// "pembaruan" ke rilis yang lebih tua (downgrade diam-diam).
//
// Tes ini hanya berjalan di dalam repo (butuh git); dilewati bila tidak.
func TestVersiSumberIkutTagTerakhir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git tidak tersedia")
	}
	tag, err := exec.Command("git", "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		t.Skip("repo tanpa tag git — lewati pemeriksaan versi")
	}
	tagTerakhir := strings.TrimSpace(string(tag))
	if tagTerakhir == "" {
		t.Skip("tag kosong")
	}

	// Baca nilai `var version = "..."` dari main.go di akar repo (file di
	// disk, bukan HEAD — agar berguna sebagai pemeriksaan pra-commit).
	repoRoot := "../.."
	if !IsSourceDir(repoRoot) {
		t.Skip("bukan dijalankan dari dalam repo")
	}
	src, err := os.ReadFile(filepath.Join(repoRoot, "main.go"))
	if err != nil {
		t.Skipf("main.go tidak terbaca: %v", err)
	}
	re := regexp.MustCompile(`(?m)^var version\s*=\s*"([^"]+)"`)
	m := re.FindSubmatch(src)
	if m == nil {
		t.Fatal("deklarasi `var version = \"...\"` tidak ditemukan di main.go")
	}
	versiSumber := string(m[1])

	if Compare(versiSumber, tagTerakhir) != 0 {
		t.Fatalf(
			"versi di main.go (%s) tidak sama dengan tag terakhir (%s).\n"+
				"Naikkan `var version` setiap membuat tag, agar binary hasil "+
				"`go build` polos tidak melaporkan versi lama dan /update "+
				"tidak menawarkan downgrade.",
			versiSumber, tagTerakhir)
	}
}
