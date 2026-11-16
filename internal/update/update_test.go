package update

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCompareVersi(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.0", "v1.1.0", 1},
		{"v1.1.0", "v1.2.0", -1},
		{"1.2.0", "v1.2.0", 0},
		{"v1.2", "v1.2.0", 0},
		{"v1.10.0", "v1.9.9", 1},
		{"v1.2.0-rc1", "v1.2.0", 0},
		{"v2.0.0", "v1.99.99", 1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q,%q)=%d, mau %d", c.a, c.b, got, c.want)
		}
	}
}

func TestNewer(t *testing.T) {
	if !Newer("1.1.0", "v1.2.0") {
		t.Error("1.2.0 seharusnya lebih baru dari 1.1.0")
	}
	if Newer("1.2.0", "v1.2.0") {
		t.Error("versi sama bukan pembaruan")
	}
	if Newer("1.3.0", "v1.2.0") {
		t.Error("versi lebih baru tidak boleh dianggap usang")
	}
}

func TestAssetName(t *testing.T) {
	got := AssetName()
	want := "kartika-agent-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if got != want {
		t.Errorf("AssetName()=%q, mau %q", got, want)
	}
	if runtime.GOOS == "linux" && got != "kartika-agent-linux-"+runtime.GOARCH {
		t.Errorf("nama aset tidak sesuai konvensi build.sh: %q", got)
	}
}

func TestFindAsset(t *testing.T) {
	r := &Release{Assets: []Asset{
		{Name: "kartika-agent-darwin-arm64"},
		{Name: AssetName()},
	}}
	a, ok := r.FindAsset()
	if !ok || a.Name != AssetName() {
		t.Errorf("FindAsset()=%q,%v — mau %q", a.Name, ok, AssetName())
	}
	empty := &Release{}
	if _, ok := empty.FindAsset(); ok {
		t.Error("rilis tanpa aset seharusnya tidak menemukan aset")
	}
}

func TestVerifySHA256(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, []byte("kartika"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, err := Sum256(p)
	if err != nil {
		t.Fatal(err)
	}

	checked, err := VerifySHA256(p, "sha256:"+sum)
	if !checked || err != nil {
		t.Errorf("checksum benar ditolak: checked=%v err=%v", checked, err)
	}
	if checked, err = VerifySHA256(p, sum); !checked || err != nil {
		t.Errorf("digest tanpa prefiks ditolak: %v %v", checked, err)
	}
	if _, err = VerifySHA256(p, "sha256:deadbeef"); err == nil {
		t.Error("checksum salah seharusnya gagal")
	}
	// digest kosong: bukan kegagalan, tetapi checked=false
	if checked, err = VerifySHA256(p, ""); checked || err != nil {
		t.Errorf("tanpa digest: checked=%v err=%v, mau false,nil", checked, err)
	}
}

func TestNotesRingkas(t *testing.T) {
	in := "baris1\nbaris2\nbaris3\nbaris4"
	got := NotesRingkas(in, 2)
	if got != "baris1\nbaris2\n…" {
		t.Errorf("NotesRingkas=%q", got)
	}
	if NotesRingkas("  satu baris  ", 5) != "satu baris" {
		t.Error("NotesRingkas harus memangkas spasi")
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		512:             "512 B",
		1024:            "1.0 KB",
		5871874:         "5.6 MB",
		3 * 1024 * 1024: "3.0 MB",
	}
	for n, want := range cases {
		if got := HumanSize(n); got != want {
			t.Errorf("HumanSize(%d)=%q, mau %q", n, got, want)
		}
	}
}

func TestIsSourceDir(t *testing.T) {
	dir := t.TempDir()
	if IsSourceDir(dir) {
		t.Error("direktori kosong bukan akar repo")
	}
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644)
	if IsSourceDir(dir) {
		t.Error("go.mod saja belum cukup")
	}
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)
	if !IsSourceDir(dir) {
		t.Error("go.mod + main.go seharusnya dikenali sebagai akar repo")
	}
	if IsSourceDir("") {
		t.Error("string kosong bukan akar repo")
	}
}

func TestInstallMenimpaBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("jalur Windows menulis .new")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "baru")
	dst := filepath.Join(dir, "kartika")
	if err := os.WriteFile(src, []byte("v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	to, err := Install(src, dst)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if to != dst {
		t.Errorf("dipasang ke %q, mau %q", to, dst)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v2" {
		t.Errorf("isi binary = %q, mau v2", data)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o100 == 0 {
		t.Error("binary hasil pemasangan harus executable")
	}
}

func TestRepoOverride(t *testing.T) {
	t.Setenv("KARTIKA_REPO", "org/lain")
	if Repo() != "org/lain" {
		t.Errorf("Repo()=%q, mau org/lain", Repo())
	}
	if PageURL() != "https://github.com/org/lain/releases" {
		t.Errorf("PageURL()=%q", PageURL())
	}
	t.Setenv("KARTIKA_REPO", "")
	if Repo() != DefaultRepo {
		t.Errorf("Repo() tanpa override = %q", Repo())
	}
}

func TestEnabled(t *testing.T) {
	for _, v := range []string{"1", "true", "YES", "on"} {
		t.Setenv("KARTIKA_NO_UPDATE_CHECK", v)
		if Enabled() {
			t.Errorf("KARTIKA_NO_UPDATE_CHECK=%q seharusnya mematikan cek", v)
		}
	}
	t.Setenv("KARTIKA_NO_UPDATE_CHECK", "")
	if !Enabled() {
		t.Error("cek update harus aktif secara default")
	}
}
