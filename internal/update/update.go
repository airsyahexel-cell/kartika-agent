// Package update — pemeriksa rilis & pemasang binary mandiri (self-update).
//
// Alur yang ditangani:
//  1. Menanyakan rilis terbaru ke GitHub API (repo bisa dioverride lewat
//     KARTIKA_REPO), dengan cache 6 jam agar startup tidak membebani API.
//  2. Mencocokkan nama aset rilis dengan platform saat ini
//     (kartika-agent-<os>-<arch>[.exe]) — nama yang sama dengan keluaran
//     build.sh / workflow release.
//  3. Mengunduh aset, memverifikasi SHA-256 dari field `digest` rilis, lalu
//     memasangnya secara atomik (tulis .new → rename) di atas binary lama.
//
// 100% Golang Standard Library — tanpa dependensi pihak ketiga.
package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DefaultRepo adalah lokasi repo rilis resmi (bisa dioverride KARTIKA_REPO).
const DefaultRepo = "airsyahexel-cell/kartika-agent"

// cacheTTL menentukan berapa lama hasil pemeriksaan rilis dipakai ulang.
const cacheTTL = 6 * time.Hour

// Repo mengembalikan "org/repo" yang dipakai untuk cek rilis.
func Repo() string {
	if r := strings.TrimSpace(os.Getenv("KARTIKA_REPO")); r != "" {
		return r
	}
	return DefaultRepo
}

// PageURL adalah alamat halaman rilis yang bisa ditunjukkan ke operator.
func PageURL() string { return "https://github.com/" + Repo() + "/releases" }

// Asset adalah satu berkas pada sebuah rilis.
type Asset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"` // "sha256:<hex>" (kosong pada rilis lama)
	URL    string `json:"browser_download_url"`
}

// Release adalah satu rilis GitHub (bagian yang kami pakai).
type Release struct {
	Tag     string    `json:"tag_name"`
	Name    string    `json:"name"`
	URL     string    `json:"html_url"`
	Notes   string    `json:"body"`
	Assets  []Asset   `json:"assets"`
	Fetched time.Time `json:"-"`
	Stale   bool      `json:"-"` // true bila diambil dari cache kedaluwarsa
}

// AssetName mengembalikan nama aset rilis untuk platform saat ini.
// Konvensi ini sama dengan build.sh dan .github/workflows/release.yml.
func AssetName() string {
	n := "kartika-agent-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		n += ".exe"
	}
	return n
}

// FindAsset mencari aset yang cocok dengan platform saat ini.
func (r *Release) FindAsset() (Asset, bool) {
	want := AssetName()
	for _, a := range r.Assets {
		if strings.EqualFold(a.Name, want) {
			return a, true
		}
	}
	return Asset{}, false
}

// ---------------------------------------------------------------------------
// Versi
// ---------------------------------------------------------------------------

// parseVer mengubah "v1.2.3", "1.2.3", "v1.2.3-rc1" menjadi [..]int.
// Bagian non-numerik (prefiks v, sufiks -rc1) diabaikan.
func parseVer(v string) []int {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			out = append(out, 0)
			continue
		}
		out = append(out, n)
	}
	return out
}

// Compare membandingkan dua versi: -1 bila a<b, 0 bila sama, 1 bila a>b.
func Compare(a, b string) int {
	pa, pb := parseVer(a), parseVer(b)
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

// Newer melaporkan apakah rilis (tag) lebih baru daripada versi berjalan.
func Newer(current, tag string) bool { return Compare(tag, current) > 0 }

// ---------------------------------------------------------------------------
// Pemeriksaan rilis (dengan cache)
// ---------------------------------------------------------------------------

// cachePath mengembalikan lokasi berkas cache hasil cek rilis.
func cachePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "kartika", "update-check.json")
}

type cacheFile struct {
	CheckedAt time.Time `json:"checked_at"`
	Release   Release   `json:"release"`
}

// Latest mengambil rilis terbaru dari GitHub (tanpa cache).
func Latest(timeout time.Duration) (*Release, error) {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	api := "https://api.github.com/repos/" + Repo() + "/releases/latest"
	req, err := http.NewRequest("GET", api, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "kartika-agent-updater")
	cl := &http.Client{Timeout: timeout}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("repo %s belum menerbitkan rilis (404)", Repo())
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API menjawab %s", resp.Status)
	}
	var r Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return nil, err
	}
	r.Fetched = time.Now()
	if r.Tag == "" {
		return nil, errors.New("rilis tidak memiliki tag versi")
	}
	return &r, nil
}

// LatestCached memakai cache bila masih segar; bila jaringan gagal tetapi
// cache lama tersedia, cache itu dikembalikan dengan Stale=true agar operator
// tetap mendapat informasi tanpa startup menggantung.
func LatestCached() (*Release, error) {
	cp := cachePath()
	if cp != "" {
		if data, err := os.ReadFile(cp); err == nil {
			var cf cacheFile
			if json.Unmarshal(data, &cf) == nil && cf.Release.Tag != "" {
				if time.Since(cf.CheckedAt) < cacheTTL {
					r := cf.Release
					r.Fetched = cf.CheckedAt
					return &r, nil
				}
			}
		}
	}
	r, err := Latest(5 * time.Second)
	if err != nil {
		// fallback: cache kedaluwarsa (bila ada)
		if cp != "" {
			if data, rerr := os.ReadFile(cp); rerr == nil {
				var cf cacheFile
				if json.Unmarshal(data, &cf) == nil && cf.Release.Tag != "" {
					rel := cf.Release
					rel.Fetched = cf.CheckedAt
					rel.Stale = true
					return &rel, nil
				}
			}
		}
		return nil, err
	}
	if cp != "" {
		if data, err := json.Marshal(cacheFile{CheckedAt: time.Now(), Release: *r}); err == nil {
			_ = os.MkdirAll(filepath.Dir(cp), 0o755)
			_ = os.WriteFile(cp, data, 0o644)
		}
	}
	return r, nil
}

// ---------------------------------------------------------------------------
// Unduh & verifikasi
// ---------------------------------------------------------------------------

// Download mengunduh url ke dst dan mengembalikan jumlah byte tertulis.
func Download(url, dst string, timeout time.Duration) (int64, error) {
	return DownloadProgress(url, dst, timeout, nil)
}

// DownloadProgress sama dengan Download, tetapi memanggil onProgress secara
// berkala (maksimal ~10x/detik) dengan jumlah byte yang sudah diterima dan
// total byte (0 bila server tidak mengirim Content-Length). Dipakai /update
// agar operator melihat kemajuan unduhan, bukan layar yang membeku.
func DownloadProgress(url, dst string, timeout time.Duration, onProgress func(done, total int64)) (int64, error) {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "kartika-agent-updater")
	cl := &http.Client{Timeout: timeout}
	resp, err := cl.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unduhan gagal: %s", resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	f, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	total := resp.ContentLength
	var src io.Reader = resp.Body
	if onProgress != nil {
		src = &progressReader{r: resp.Body, total: total, cb: onProgress}
	}
	n, err := io.Copy(f, src)
	cerr := f.Close()
	if err != nil {
		return n, err
	}
	if cerr != nil {
		return n, cerr
	}
	if total > 0 && n != total {
		return n, fmt.Errorf("unduhan terpotong: %d dari %d byte", n, total)
	}
	if onProgress != nil {
		onProgress(n, total)
	}
	return n, nil
}

// progressReader melaporkan kemajuan unduhan ke callback, dibatasi ~10
// panggilan per detik agar tidak membanjiri layar.
type progressReader struct {
	r     io.Reader
	done  int64
	total int64
	cb    func(done, total int64)
	last  time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if p.cb != nil && (p.last.IsZero() || time.Since(p.last) >= 100*time.Millisecond || err != nil) {
		p.last = time.Now()
		p.cb(p.done, p.total)
	}
	return n, err
}

// Sum256 menghitung SHA-256 sebuah berkas dalam bentuk hex.
func Sum256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifySHA256 memeriksa berkas terhadap digest rilis ("sha256:<hex>").
// Digest kosong berarti rilis tidak menyediakan checksum — bukan kegagalan
// verifikasi, tetapi pemanggil wajib memberi tahu operator.
func VerifySHA256(path, digest string) (checked bool, err error) {
	digest = strings.TrimSpace(digest)
	if digest == "" {
		return false, nil
	}
	want := digest
	if i := strings.Index(digest, ":"); i >= 0 {
		want = digest[i+1:]
	}
	got, err := Sum256(path)
	if err != nil {
		return true, err
	}
	if !strings.EqualFold(got, want) {
		return true, fmt.Errorf("checksum TIDAK cocok (diharapkan %s, diterima %s)", short(want), short(got))
	}
	return true, nil
}

// short memendekkan nilai hash untuk ditampilkan.
func short(h string) string {
	if len(h) > 16 {
		return h[:16] + "…"
	}
	return h
}

// ---------------------------------------------------------------------------
// Pemasangan
// ---------------------------------------------------------------------------

// Install memasang src sebagai binary pengganti dst secara atomik.
//
// Bila direktori dst tidak dapat ditulis (mis. /usr/local/bin tanpa sudo),
// binary dipasang ke ~/.local/bin dan path itu dikembalikan agar operator
// tahu lokasi barunya. Pada Windows, binary yang sedang berjalan tidak bisa
// ditimpa: berkas baru ditulis dengan akhiran .new.exe di sebelah dst dan
// pemanggil diminta menukarnya setelah keluar.
func Install(src, dst string) (installedTo string, err error) {
	if dst == "" {
		return "", errors.New("lokasi binary tidak diketahui")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil && !os.IsExist(err) {
		// lanjut: mungkin direktori sudah ada tetapi tidak bisa dibuat ulang
	}
	dir := filepath.Dir(dst)

	// Windows: tidak bisa menimpa exe yang sedang berjalan.
	if runtime.GOOS == "windows" {
		newPath := dst + ".new"
		if err := copyFile(src, newPath); err != nil {
			return "", err
		}
		return newPath, nil
	}

	target, err := writableTarget(dir, dst)
	if err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp(filepath.Dir(target), ".kartika-new-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpName)

	if err := copyFile(src, tmpName); err != nil {
		return "", err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return "", err
	}
	// rename atomik: reader lama tetap melihat inode lama sampai selesai.
	if err := os.Rename(tmpName, target); err != nil {
		if target != dst {
			return "", fmt.Errorf("gagal memasang ke %s: %w", target, err)
		}
		// direktori milik root → jatuh ke ~/.local/bin
		fallback, ferr := localBin()
		if ferr != nil {
			return "", fmt.Errorf("gagal menimpa %s (coba sudo) : %w", dst, err)
		}
		if err2 := copyFile(tmpName, fallback); err2 != nil {
			return "", fmt.Errorf("gagal memasang ke %s: %w", fallback, err2)
		}
		_ = os.Chmod(fallback, 0o755)
		return fallback, nil
	}
	return target, nil
}

// writableTarget memilih direktori tujuan yang bisa ditulis.
func writableTarget(dir, dst string) (string, error) {
	f, err := os.CreateTemp(dir, ".kartika-probe-*")
	if err == nil {
		name := f.Name()
		f.Close()
		os.Remove(name)
		return dst, nil
	}
	fallback, ferr := localBin()
	if ferr != nil {
		return "", fmt.Errorf("direktori %s tidak dapat ditulis dan tidak ada ~/.local/bin", dir)
	}
	return fallback, nil
}

// localBin mengembalikan ~/.local/bin/kartika (dibuat bila perlu).
func localBin() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("HOME tidak diketahui")
	}
	dir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := "kartika"
	if runtime.GOOS == "windows" {
		name = "kartika.exe"
	}
	return filepath.Join(dir, name), nil
}

// copyFile menyalin src → dst (menimpa) dengan izin 0755.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// ---------------------------------------------------------------------------
// Build dari source (opsi "build ulang" pada /update)
// ---------------------------------------------------------------------------

// IsSourceDir melaporkan apakah dir tampak seperti akar repo kartika-agent
// (memiliki go.mod dan main.go).
func IsSourceDir(dir string) bool {
	if dir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "main.go")); err != nil {
		return false
	}
	return true
}

// BuildFromSource membangun binary dari kode sumber di srcDir ke outPath
// memakai toolchain Go yang terpasang. Versi ditanam lewat -X main.version
// agar binary hasil build sendiri melaporkan versi yang benar.
func BuildFromSource(srcDir, outPath, ver string) error {
	if !IsSourceDir(srcDir) {
		return fmt.Errorf("%s bukan direktori sumber kartika-agent (go.mod/main.go tidak ditemukan)", srcDir)
	}
	goBin := "go"
	if _, err := exec.LookPath(goBin); err != nil {
		return errors.New("toolchain Go tidak ditemukan di PATH — pasang Go ≥ 1.21 atau pilih pasang binary rilis")
	}
	ldflags := "-s -w"
	if ver != "" {
		ldflags += " -X main.version=" + ver
	}
	cmd := exec.Command(goBin, "build", "-ldflags="+ldflags, "-o", outPath, ".")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}

// CurrentExecutable mengembalikan path binary yang sedang berjalan
// (symlink diselesaikan) — sasaran penggantian saat /update.
func CurrentExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}

// Enabled melaporkan apakah pemeriksaan update otomatis boleh berjalan
// (dimatikan lewat KARTIKA_NO_UPDATE_CHECK=1 — berguna untuk CI/air-gapped).
func Enabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("KARTIKA_NO_UPDATE_CHECK")))
	return !(v == "1" || v == "true" || v == "yes" || v == "on")
}

// NotesRingkas memotong catatan rilis agar muat di terminal (maks n baris).
func NotesRingkas(notes string, n int) string {
	if n <= 0 {
		n = 12
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(notes, "\r\n", "\n")), "\n")
	if len(lines) > n {
		lines = append(lines[:n], "…")
	}
	return strings.Join(lines, "\n")
}

// HumanSize memformat ukuran byte menjadi teks pendek (mis. "5.7 MB").
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
