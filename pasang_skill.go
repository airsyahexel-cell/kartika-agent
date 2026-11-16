// pasang_skill.go — pemasangan pustaka SKILL langsung dari dalam posko.
//
// Alur yang ditangani:
//
//	/skill                      → daftar skill + menu "＋ Pasang skill baru"
//	/skill pasang [url|owner/repo] → clone repositori skill ke pustaka
//	/skill perbarui <nama>      → git pull untuk repositori yang sudah ada
//
// Format yang didukung adalah Agent Skills standar: <root>/<repo>/<nama>/SKILL.md
// (frontmatter YAML name + description), sehingga repo hasil clone langsung
// terbaca tanpa langkah konfigurasi tambahan.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"kartika-agent/internal/tools"
	"kartika-agent/internal/ui"
)

// repoSaranSkill adalah repositori skill yang paling sering dipakai.
// Ditampilkan sebagai pilihan cepat agar operator tidak perlu mengetik URL.
var repoSaranSkill = []struct {
	Nama string
	URL  string
	Desc string
}{
	{
		Nama: "bug-bounty-agent-skills",
		URL:  "https://github.com/murrtada/bug-bounty-agent-skills.git",
		Desc: "pustaka metodologi bug bounty & red team (hunt-*, triage, report)",
	},
	{
		Nama: "anthropic-skills",
		URL:  "https://github.com/anthropics/skills.git",
		Desc: "contoh Agent Skills resmi Anthropic (pdf, docx, xlsx, canvas)",
	},
}

// normalisasiRepoURL menerima beberapa bentuk masukan operator dan
// mengembalikannya sebagai URL git yang siap di-clone:
//
//	owner/repo                          → https://github.com/owner/repo.git
//	github.com/owner/repo               → https://github.com/owner/repo.git
//	https://github.com/owner/repo       → https://github.com/owner/repo.git
//	git@github.com:owner/repo.git       → dibiarkan (SSH)
func normalisasiRepoURL(in string) string {
	s := strings.TrimSpace(in)
	s = strings.Trim(s, `"'`)
	if s == "" {
		return ""
	}
	// SSH / skema lain: pakai apa adanya.
	if strings.HasPrefix(s, "git@") || strings.HasPrefix(s, "ssh://") || strings.HasPrefix(s, "git://") {
		return s
	}
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	// github.com/owner/repo → owner/repo
	if strings.HasPrefix(s, "github.com/") {
		s = strings.TrimPrefix(s, "github.com/")
	}
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	if i := strings.IndexByte(s, '/'); i <= 0 || i >= len(s)-1 {
		return "" // bukan bentuk owner/repo
	}
	return "https://github.com/" + s + ".git"
}

// namaRepoDariURL mengambil komponen repo dari URL git untuk dipakai
// sebagai nama direktori tujuan.
func namaRepoDariURL(url string) string {
	s := strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git")
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// handleSkill memasang / memperbarui pustaka skill.
//
// Tanpa argumen: tampilkan pustaka + tawarkan pemasangan (bila ada picker).
// Dengan argumen: "pasang <repo>" atau "perbarui <nama>".
func handleSkill(arg string, readInput func(string) (string, error),
	appendMsg func(string), pick pickFn) {

	fields := strings.Fields(strings.TrimSpace(arg))
	sub := ""
	if len(fields) > 0 {
		sub = strings.ToLower(fields[0])
	}

	switch sub {
	case "pasang", "install", "add", "tambah":
		repo := ""
		if len(fields) > 1 {
			repo = strings.Join(fields[1:], " ")
		}
		pasangSkill(repo, readInput, appendMsg, pick)
		return
	case "perbarui", "update", "pull", "upgrade":
		nama := ""
		if len(fields) > 1 {
			nama = fields[1]
		}
		perbaruiSkill(nama, readInput, appendMsg, pick)
		return
	}

	// Tanpa sub-perintah: tampilkan pustaka lalu tawarkan pemasangan.
	printSkillList()
	menuSkill(readInput, appendMsg, pick)
}

// menuSkill menampilkan pilihan pemasangan dan menjalankan aksinya.
// Dipakai /skill dari TUI (yang punya picker) agar fitur pasang mudah
// ditemukan tanpa harus hafal sub-perintah.
func menuSkill(readInput func(string) (string, error), appendMsg func(string), pick pickFn) {
	if pick == nil {
		printSkillList()
		appendMsg(ui.Dim("  Pasang pustaka skill: /skill pasang <url-github>"))
		return
	}
	pilihan, ok := pick("PUSTAKA SKILL", []ui.PickerItem{
		{Label: "＋ Pasang skill baru", Desc: "clone repositori skill dari GitHub"},
		{Label: "↻ Perbarui skill terpasang", Desc: "git pull pustaka yang sudah ada"},
		{Label: "◈ Daftar skill", Desc: "tampilkan pustaka saat ini"},
	})
	if !ok {
		appendMsg(ui.Dim("  Dibatalkan."))
		return
	}
	switch {
	case strings.HasPrefix(pilihan, "＋"):
		pasangSkill("", readInput, appendMsg, pick)
	case strings.HasPrefix(pilihan, "↻"):
		perbaruiSkill("", readInput, appendMsg, pick)
	default:
		printSkillList()
	}
}

// pasangSkill meng-clone repositori skill ke pustaka lalu memindai ulang.
// repo kosong → tampilkan pilihan repositori yang disarankan.
func pasangSkill(repo string, readInput func(string) (string, error),
	appendMsg func(string), pick pickFn) {

	appendMsg("")
	appendMsg(ui.Bold(ui.Cyan("┌─ PASANG PUSTAKA SKILL")))
	appendMsg(ui.Dim("  Tujuan : " + tools.SkillsRoot()))
	appendMsg("")

	if _, err := exec.LookPath("git"); err != nil {
		appendMsg(ui.Red("✗ git tidak ditemukan di PATH."))
		appendMsg(ui.Dim("  Pasang git lebih dulu (mis. sudo apt install git), lalu ulangi."))
		appendMsg("")
		return
	}

	// Pilih sumber repositori.
	if repo == "" {
		items := make([]ui.PickerItem, 0, len(repoSaranSkill)+1)
		for _, r := range repoSaranSkill {
			items = append(items, ui.PickerItem{Label: r.Nama, Desc: r.Desc})
		}
		val, ok := chooseAtauManual(pick, readInput, appendMsg,
			"PILIH REPOSITORI SKILL", items,
			"URL repositori lain (github.com/owner/repo)", "REPO ❯")
		if !ok {
			appendMsg(ui.Dim("  Pemasangan skill dibatalkan — pustaka lama tetap dipakai."))
			appendMsg("")
			return
		}
		repo = val
		for _, r := range repoSaranSkill {
			if strings.EqualFold(val, r.Nama) {
				repo = r.URL
				break
			}
		}
	}

	url := normalisasiRepoURL(repo)
	if url == "" {
		appendMsg(ui.Red("✗ Bentuk repositori tidak dikenali: " + repo))
		appendMsg(ui.Dim("  Contoh yang benar: owner/repo atau https://github.com/owner/repo"))
		appendMsg("")
		return
	}
	nama := namaRepoDariURL(url)
	if nama == "" {
		appendMsg(ui.Red("✗ Nama repositori tidak dapat dibaca dari URL: " + url))
		appendMsg("")
		return
	}

	root := tools.SkillsRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		appendMsg(ui.Red("✗ Tidak dapat membuat direktori pustaka: " + err.Error()))
		appendMsg("")
		return
	}

	dst := filepath.Join(root, nama)
	if _, err := os.Stat(dst); err == nil {
		// Sudah ada → tawarkan pembaruan, jangan menimpa diam-diam.
		appendMsg(ui.Yellow("! Repositori '" + nama + "' sudah ada di pustaka."))
		lanjut, ok := konfirmasi(readInput, appendMsg, "Perbarui (git pull) sekarang? [y/N] ❯")
		if !ok || !lanjut {
			appendMsg(ui.Dim("  Tidak ada perubahan — pustaka lama tetap dipakai."))
			appendMsg("")
			return
		}
		gitPull(dst, appendMsg)
		selesaiPemasanganSkill(appendMsg)
		return
	}

	appendMsg("  Meng-clone " + ui.Cyan(url) + " …")
	appendMsg(ui.Dim("  (repositori besar bisa butuh beberapa saat)"))
	cmd := exec.Command("git", "clone", "--depth", "1", url, dst)
	out, err := cmd.CombinedOutput()
	if err != nil {
		appendMsg(ui.Red("✗ git clone gagal:"))
		for _, l := range ringkasBaris(string(out), 6) {
			appendMsg(ui.Dim("    " + l))
		}
		// Bersihkan sisa direktori agar percobaan berikutnya tidak bentrok.
		if _, statErr := os.Stat(dst); statErr == nil {
			_ = os.RemoveAll(dst)
		}
		appendMsg("")
		return
	}
	appendMsg(ui.Green("✓ Ter-clone: " + dst))
	selesaiPemasanganSkill(appendMsg)
}

// perbaruiSkill menjalankan git pull pada satu (atau seluruh) repositori
// skill yang sudah terpasang di pustaka.
func perbaruiSkill(nama string, readInput func(string) (string, error),
	appendMsg func(string), pick pickFn) {

	appendMsg("")
	appendMsg(ui.Bold(ui.Cyan("┌─ PERBARUI PUSTAKA SKILL")))

	root := tools.SkillsRoot()
	repos := daftarRepoSkill(root)
	if len(repos) == 0 {
		appendMsg(ui.Yellow("  Belum ada repositori skill terpasang."))
		appendMsg(ui.Dim("  Pasang dulu: /skill pasang <owner/repo>"))
		appendMsg("")
		return
	}
	if _, err := exec.LookPath("git"); err != nil {
		appendMsg(ui.Red("✗ git tidak ditemukan di PATH."))
		appendMsg("")
		return
	}

	targets := repos
	if nama != "" {
		targets = nil
		for _, r := range repos {
			if strings.EqualFold(r, nama) {
				targets = append(targets, r)
			}
		}
		if len(targets) == 0 {
			appendMsg(ui.Red("✗ Repositori '" + nama + "' tidak ada di pustaka."))
			appendMsg(ui.Dim("  Tersedia: " + strings.Join(repos, ", ")))
			appendMsg("")
			return
		}
	} else if len(repos) > 1 && pick != nil {
		items := make([]ui.PickerItem, 0, len(repos)+1)
		items = append(items, ui.PickerItem{Label: "↻ Semua repositori", Desc: fmt.Sprintf("%d repositori", len(repos))})
		for _, r := range repos {
			items = append(items, ui.PickerItem{Label: r, Desc: "git pull " + filepath.Join(root, r)})
		}
		val, ok := pick("PERBARUI SKILL", items)
		if !ok {
			appendMsg(ui.Dim("  Dibatalkan."))
			appendMsg("")
			return
		}
		if strings.HasPrefix(val, "↻") {
			targets = repos
		} else {
			targets = []string{val}
		}
	}

	for _, r := range targets {
		appendMsg("")
		appendMsg("  " + ui.Bold(r))
		gitPull(filepath.Join(root, r), appendMsg)
	}
	selesaiPemasanganSkill(appendMsg)
}

// gitPull menjalankan "git pull --ff-only" pada satu direktori repositori.
func gitPull(dir string, appendMsg func(string)) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		appendMsg(ui.Dim("    bukan repositori git — dilewati"))
		return
	}
	cmd := exec.Command("git", "-C", dir, "pull", "--ff-only")
	out, err := cmd.CombinedOutput()
	if err != nil {
		appendMsg(ui.Yellow("    ! git pull gagal (riwayat lokal berubah?)"))
		for _, l := range ringkasBaris(string(out), 4) {
			appendMsg(ui.Dim("      " + l))
		}
		return
	}
	pesan := strings.TrimSpace(string(out))
	if pesan == "" || strings.Contains(pesan, "Already up to date") {
		appendMsg(ui.Dim("    ✓ sudah versi terbaru"))
		return
	}
	for _, l := range ringkasBaris(pesan, 4) {
		appendMsg(ui.Dim("    " + l))
	}
}

// selesaiPemasanganSkill memindai ulang pustaka dan melaporkan hasilnya,
// sehingga skill yang baru dipasang langsung bisa dipakai tanpa restart.
func selesaiPemasanganSkill(appendMsg func(string)) {
	tools.RefreshSkills()
	skills := tools.SkillsAll()
	appendMsg("")
	if len(skills) == 0 {
		appendMsg(ui.Yellow("  Repositori terpasang, tetapi tidak ada SKILL.md yang ditemukan."))
		appendMsg(ui.Dim("  Pastikan repo memakai format Agent Skills: <nama>/SKILL.md berfrontmatter name+description."))
		appendMsg("")
		return
	}
	appendMsg(ui.Bold(ui.Green(fmt.Sprintf("✓ Pustaka skill siap: %d skill terdeteksi", len(skills)))))
	contoh := skills
	if len(contoh) > 6 {
		contoh = contoh[:6]
	}
	names := make([]string, 0, len(contoh))
	for _, s := range contoh {
		names = append(names, s.Name)
	}
	appendMsg(ui.Dim("  " + strings.Join(names, ", ")))
	appendMsg(ui.Dim("  Baca metodologi: /skill <nama>  ·  daftar: /skill"))
	appendMsg("")
}

// daftarRepoSkill mengembalikan nama sub-direktori pustaka yang merupakan
// repositori git (kandidat untuk git pull).
func daftarRepoSkill(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, e.Name(), ".git")); err == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// konfirmasi menanyakan pertanyaan ya/tidak; default TIDAK (Enter = tidak).
func konfirmasi(readInput func(string) (string, error), appendMsg func(string), prompt string) (bool, bool) {
	in, err := readInput(prompt)
	if err != nil {
		return false, false
	}
	s := strings.ToLower(strings.TrimSpace(in))
	return s == "y" || s == "ya" || s == "yes", true
}

// konfirmasiYa menanyakan pertanyaan ya/tidak; default YA (Enter = ya).
func konfirmasiYa(readInput func(string) (string, error), appendMsg func(string), prompt string) (bool, bool) {
	in, err := readInput(prompt)
	if err != nil {
		return false, false
	}
	s := strings.ToLower(strings.TrimSpace(in))
	if s == "" {
		return true, true
	}
	return s == "y" || s == "ya" || s == "yes", true
}

// ringkasBaris mengambil n baris pertama yang tidak kosong dari keluaran
// perintah, agar pesan galat panjang tetap muat di layar.
func ringkasBaris(s string, n int) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		out = append(out, l)
		if len(out) >= n {
			break
		}
	}
	return out
}
