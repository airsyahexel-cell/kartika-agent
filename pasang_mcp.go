// pasang_mcp.go — penambahan, pengujian, dan penghapusan MCP server
// langsung dari dalam posko, tanpa menyunting mcp.json secara manual.
//
// Alur yang ditangani:
//
//	/mcp                         → status + menu (tambah/uji/hapus/segarkan)
//	/mcp tambah                  → wizard: nama → perintah → argumen → env
//	/mcp tambah <nama> <perintah> [args…]
//	/mcp hapus <nama>            → hapus entri dari mcp.json
//	/mcp uji <nama>              → jalankan server sebentar, laporkan tools
//
// Entri yang ditulis memakai bentuk standar mcp.json:
//
//	{"mcpServers": {"<nama>": {"command": "npx", "args": [...], "env": {...}}}}
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"kartika-agent/internal/config"
	"kartika-agent/internal/mcp"
	"kartika-agent/internal/tools"
	"kartika-agent/internal/ui"
)

// mcpSaran adalah server MCP populer — dipakai sebagai pilihan cepat agar
// operator tidak perlu menghafal nama paket npx/uvx.
var mcpSaran = []struct {
	Nama string
	Cmd  string
	Args []string
	Desc string
}{
	{
		Nama: "filesystem",
		Cmd:  "npx",
		Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "."},
		Desc: "baca/tulis berkas pada direktori kerja",
	},
	{
		Nama: "git",
		Cmd:  "uvx",
		Args: []string{"mcp-server-git", "--repository", "."},
		Desc: "inspeksi repositori git lokal (log, diff, blame)",
	},
	{
		Nama: "sqlite",
		Cmd:  "uvx",
		Args: []string{"mcp-server-sqlite", "--db-path", "./app.db"},
		Desc: "kueri database SQLite",
	},
	{
		Nama: "playwright",
		Cmd:  "npx",
		Args: []string{"-y", "@playwright/mcp@latest", "--browser", "chromium"},
		Desc: "otomasi browser (render JS, screenshot, uji UI)",
	},
	{
		Nama: "fetch",
		Cmd:  "uvx",
		Args: []string{"mcp-server-fetch"},
		Desc: "ambil & ringkas halaman web",
	},
}

// handleMCPStatusAtauMenu menampilkan status MCP, lalu (bila picker ada)
// menawarkan aksi tambah/uji/hapus. Mengembalikan true bila mcp.json
// berubah — pemanggil lalu memuat ulang server agar tools baru terdaftar.
func handleMCPStatusAtauMenu(cfg *config.Config, clients []*mcp.Client,
	readInput func(string) (string, error), appendMsg func(string), pick pickFn) bool {

	if clients == nil {
		clients = loadMCPQuiet(cfg)
	}
	printMCPStatus(cfg, clients)
	if pick == nil {
		appendMsg(ui.Dim("  Tambah server: /mcp tambah · Hapus: /mcp hapus <nama> · Uji: /mcp uji <nama>"))
		return false
	}
	items := []ui.PickerItem{
		{Label: "＋ Tambah server MCP", Desc: "wizard: nama, perintah, argumen"},
		{Label: "◈ Uji jalankan server", Desc: "pastikan perintah & tools-nya bekerja"},
	}
	if len(daftarServerMCP(cfg)) > 0 {
		items = append(items, ui.PickerItem{Label: "✕ Hapus server", Desc: "hapus entri dari mcp.json"})
	}
	items = append(items, ui.PickerItem{Label: "✕ Tutup", Desc: "kembali ke chat"})

	pilihan, ok := pick("MCP SERVER", items)
	if !ok {
		return false
	}
	switch {
	case strings.HasPrefix(pilihan, "＋"):
		return tambahMCP("", readInput, appendMsg, pick)
	case strings.HasPrefix(pilihan, "◈"):
		ujiMCP(cfg, "", readInput, appendMsg, pick)
		return false
	case strings.HasPrefix(pilihan, "✕ Hapus"):
		return hapusMCP(cfg, "", readInput, appendMsg, pick)
	}
	return false
}

// handleMCP menangani sub-perintah /mcp: tambah | hapus | uji | daftar.
// Mengembalikan true bila mcp.json berubah.
func handleMCP(arg string, cfg *config.Config, readInput func(string) (string, error),
	appendMsg func(string), pick pickFn) bool {

	fields := strings.Fields(strings.TrimSpace(arg))
	if len(fields) == 0 {
		return handleMCPStatusAtauMenu(cfg, nil, readInput, appendMsg, pick)
	}
	sub := strings.ToLower(fields[0])
	rest := fields[1:]

	switch sub {
	case "tambah", "add", "pasang", "install":
		return tambahMCP(strings.Join(rest, " "), readInput, appendMsg, pick)
	case "hapus", "remove", "rm", "delete":
		return hapusMCP(cfg, strings.Join(rest, " "), readInput, appendMsg, pick)
	case "uji", "test", "jalankan", "run":
		ujiMCP(cfg, strings.Join(rest, " "), readInput, appendMsg, pick)
		return false
	case "daftar", "list", "ls":
		tampilkanDaftarMCP(cfg, appendMsg)
		return false
	default:
		appendMsg(ui.Yellow("  Sub-perintah /mcp tidak dikenal: " + sub))
		appendMsg(ui.Dim("  Gunakan: /mcp tambah · /mcp uji <nama> · /mcp hapus <nama> · /mcp daftar"))
		return false
	}
}

// loadMCPQuiet memuat server MCP hanya untuk membaca status (tanpa
// mendaftarkan tools ke registri).
func loadMCPQuiet(cfg *config.Config) []*mcp.Client {
	servers, err := bacaMCPFile(configPathMCP(cfg))
	if err != nil || len(servers) == 0 {
		return nil
	}
	names := make([]string, 0, len(servers))
	for n := range servers {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []*mcp.Client
	for _, n := range names {
		cl, err := mcp.Start(n, servers[n])
		if err != nil {
			continue
		}
		out = append(out, cl)
	}
	return out
}

// configPathMCP mengembalikan lokasi mcp.json yang dipakai.
func configPathMCP(cfg *config.Config) string {
	if cfg != nil && strings.TrimSpace(cfg.MCPConfig) != "" {
		return cfg.MCPConfig
	}
	return "mcp.json"
}

// bacaMCPFile membaca mcp.json menjadi map; berkas tidak ada = map kosong.
func bacaMCPFile(path string) (map[string]mcp.ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]mcp.ServerConfig{}, nil
		}
		return nil, err
	}
	var cf struct {
		MCPServers map[string]mcp.ServerConfig `json:"mcpServers"`
	}
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &cf); err != nil {
			return nil, fmt.Errorf("mcp.json tidak valid: %w", err)
		}
	}
	if cf.MCPServers == nil {
		cf.MCPServers = map[string]mcp.ServerConfig{}
	}
	return cf.MCPServers, nil
}

// tulisMCPFile menyimpan map server kembali ke mcp.json secara atomik
// (tulis .tmp lalu rename) agar berkas tidak rusak bila proses terhenti.
func tulisMCPFile(path string, servers map[string]mcp.ServerConfig) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(struct {
		MCPServers map[string]mcp.ServerConfig `json:"mcpServers"`
	}{servers}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".mcp-*.tmp")
	if err != nil {
		// fallback: tulis langsung bila direktori tidak bisa membuat tmp
		return os.WriteFile(path, data, 0o644)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// daftarServerMCP mengembalikan nama server yang terdaftar di mcp.json.
func daftarServerMCP(cfg *config.Config) []string {
	servers, err := bacaMCPFile(configPathMCP(cfg))
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(servers))
	for n := range servers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// tampilkanDaftarMCP menampilkan tabel [ONLINE]/[TERDAFTAR] server MCP.
func tampilkanDaftarMCP(cfg *config.Config, appendMsg func(string)) {
	path := configPathMCP(cfg)
	servers, err := bacaMCPFile(path)
	appendMsg("")
	appendMsg(ui.Bold(ui.Cyan("DAFTAR MCP SERVER")))
	appendMsg(ui.Dim("  Konfigurasi: " + path))
	if err != nil {
		appendMsg(ui.Red("  ✗ " + err.Error()))
		appendMsg("")
		return
	}
	if len(servers) == 0 {
		appendMsg("  (belum ada server terdaftar)")
		appendMsg(ui.Dim("  Tambah: /mcp tambah"))
		appendMsg("")
		return
	}
	names := make([]string, 0, len(servers))
	for n := range servers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sc := servers[n]
		args := strings.Join(sc.Args, " ")
		if rn := []rune(args); len(rn) > 60 {
			args = string(rn[:60]) + "…"
		}
		appendMsg(fmt.Sprintf("  %s %-12s %s %s",
			ui.Cyan("◈"), ui.Bold(n), ui.Dim(sc.Command), ui.Dim(args)))
	}
	appendMsg("")
}

// tambahMCP menjalankan wizard penambahan server MCP.
// spec: "<nama> <perintah> [args…]" (opsional).
func tambahMCP(spec string, readInput func(string) (string, error),
	appendMsg func(string), pick pickFn) bool {

	appendMsg("")
	appendMsg(ui.Bold(ui.Cyan("┌─ TAMBAH MCP SERVER")))
	appendMsg(ui.Dim("  Konfigurasi: " + configPathMCP(nil)))
	appendMsg(ui.Dim("  (Ctrl+C untuk membatalkan — sesi chat tetap hidup)"))
	appendMsg("")

	var nama, perintah string
	var args []string

	fields := strings.Fields(spec)
	if len(fields) >= 2 {
		nama, perintah, args = fields[0], fields[1], fields[2:]
	}

	if nama == "" {
		// Pilihan cepat server populer, atau ketik nama sendiri.
		items := make([]ui.PickerItem, 0, len(mcpSaran)+1)
		for _, s := range mcpSaran {
			items = append(items, ui.PickerItem{
				Label: s.Nama,
				Desc:  s.Cmd + " " + strings.Join(s.Args, " "),
			})
		}
		val, ok := chooseAtauManual(pick, readInput, appendMsg,
			"PILIH SERVER MCP", items,
			"nama server baru (mis. my-server)", "NAMA ❯")
		if !ok {
			appendMsg(ui.Dim("  Dibatalkan — mcp.json tidak diubah."))
			appendMsg("")
			return false
		}
		nama = val
		// Bila nama cocok dengan saran, pakai perintah & argumennya.
		for _, s := range mcpSaran {
			if strings.EqualFold(val, s.Nama) {
				perintah = s.Cmd
				args = append([]string(nil), s.Args...)
				break
			}
		}
	}

	if perintah == "" {
		masuk, err := readInput("PERINTAH ❯ (mis. npx / uvx / node)")
		if err != nil {
			if err == ui.ErrBatal {
				appendMsg(ui.Dim("  Dibatalkan — mcp.json tidak diubah."))
				appendMsg("")
				return false
			}
			appendMsg(ui.Red("✗ " + err.Error()))
			return false
		}
		perintah = strings.TrimSpace(masuk)
		if perintah == "" {
			appendMsg(ui.Dim("  Dibatalkan — perintah kosong."))
			appendMsg("")
			return false
		}
	}

	if len(args) == 0 {
		masuk, err := readInput("ARGUMEN ❯ (pisah spasi; kosongkan bila tidak ada)")
		if err != nil && err != ui.ErrBatal {
			appendMsg(ui.Red("✗ " + err.Error()))
			return false
		}
		args = strings.Fields(masuk)
	}

	appendMsg("")
	appendMsg("  Ringkasan entri:")
	appendMsg(ui.Dim(fmt.Sprintf("    %s → %s", nama, perintah)))
	if len(args) > 0 {
		appendMsg(ui.Dim("    argumen: " + strings.Join(args, " ")))
	}
	lanjut, ok := konfirmasiYa(readInput, appendMsg, "Simpan ke mcp.json? [Y/n] ❯")
	if !ok {
		appendMsg(ui.Dim("  Dibatalkan — mcp.json tidak diubah."))
		appendMsg("")
		return false
	}
	if !lanjut {
		appendMsg(ui.Dim("  Tidak disimpan."))
		appendMsg("")
		return false
	}

	path := configPathMCP(nil)
	servers, err := bacaMCPFile(path)
	if err != nil {
		appendMsg(ui.Red("✗ " + err.Error()))
		appendMsg("")
		return false
	}
	if _, ada := servers[nama]; ada {
		appendMsg(ui.Yellow("! Server '" + nama + "' sudah ada — entri ditimpa."))
	}
	servers[nama] = mcp.ServerConfig{Command: perintah, Args: args}
	if err := tulisMCPFile(path, servers); err != nil {
		appendMsg(ui.Red("✗ Gagal menyimpan: " + err.Error()))
		appendMsg("")
		return false
	}
	appendMsg(ui.Green("✓ Tersimpan: " + path))
	appendMsg("")

	// Langsung uji supaya operator tahu perintahnya benar sebelum restart.
	if ok2, _ := konfirmasiYa(readInput, appendMsg, "Uji jalankan server sekarang? [Y/n] ❯"); ok2 {
		jalankanUjiMCP(nama, servers[nama], appendMsg)
	}
	appendMsg(ui.Dim("  Server aktif setelah kartika dijalankan ulang (mcp.json dibaca saat start)."))
	appendMsg("")
	return true
}

// hapusMCP menghapus satu server dari mcp.json.
func hapusMCP(cfg *config.Config, nama string, readInput func(string) (string, error),
	appendMsg func(string), pick pickFn) bool {

	servers, err := bacaMCPFile(configPathMCP(cfg))
	if err != nil {
		appendMsg(ui.Red("✗ " + err.Error()))
		return false
	}
	if len(servers) == 0 {
		appendMsg(ui.Yellow("  Belum ada server MCP terdaftar."))
		return false
	}
	if nama == "" {
		names := make([]string, 0, len(servers))
		for n := range servers {
			names = append(names, n)
		}
		sort.Strings(names)
		items := make([]ui.PickerItem, 0, len(names))
		for _, n := range names {
			items = append(items, ui.PickerItem{Label: n, Desc: servers[n].Command})
		}
		val, ok := pick("HAPUS SERVER MCP", items)
		if !ok {
			appendMsg(ui.Dim("  Dibatalkan."))
			return false
		}
		nama = val
	}
	if _, ada := servers[nama]; !ada {
		appendMsg(ui.Red("✗ Server '" + nama + "' tidak terdaftar."))
		return false
	}
	lanjut, ok := konfirmasi(readInput, appendMsg, "Hapus '"+nama+"' dari mcp.json? [y/N] ❯")
	if !ok || !lanjut {
		appendMsg(ui.Dim("  Tidak dihapus."))
		return false
	}
	delete(servers, nama)
	if err := tulisMCPFile(configPathMCP(cfg), servers); err != nil {
		appendMsg(ui.Red("✗ Gagal menyimpan: " + err.Error()))
		return false
	}
	appendMsg(ui.Green("✓ '" + nama + "' dihapus dari mcp.json"))
	appendMsg(ui.Dim("  Berlaku setelah kartika dijalankan ulang."))
	return true
}

// ujiMCP memilih server lalu menjalankannya untuk verifikasi.
func ujiMCP(cfg *config.Config, nama string, readInput func(string) (string, error),
	appendMsg func(string), pick pickFn) {

	servers, err := bacaMCPFile(configPathMCP(cfg))
	if err != nil {
		appendMsg(ui.Red("✗ " + err.Error()))
		return
	}
	if len(servers) == 0 {
		appendMsg(ui.Yellow("  Belum ada server MCP terdaftar — tambah dengan /mcp tambah"))
		return
	}
	if nama == "" {
		names := make([]string, 0, len(servers))
		for n := range servers {
			names = append(names, n)
		}
		sort.Strings(names)
		items := make([]ui.PickerItem, 0, len(names))
		for _, n := range names {
			items = append(items, ui.PickerItem{Label: n, Desc: servers[n].Command})
		}
		val, ok := pick("UJI SERVER MCP", items)
		if !ok {
			appendMsg(ui.Dim("  Dibatalkan."))
			return
		}
		nama = val
	}
	sc, ada := servers[nama]
	if !ada {
		appendMsg(ui.Red("✗ Server '" + nama + "' tidak terdaftar."))
		return
	}
	jalankanUjiMCP(nama, sc, appendMsg)
}

// jalankanUjiMCP memulai server MCP sebentar, melaporkan tools yang
// ditemukan, lalu mematikannya kembali.
func jalankanUjiMCP(nama string, sc mcp.ServerConfig, appendMsg func(string)) {
	appendMsg("")
	appendMsg("  Menguji " + ui.Bold(nama) + " → " + ui.Dim(sc.Command+" "+strings.Join(sc.Args, " ")))
	appendMsg(ui.Dim("  (proses anak dijalankan, daftar tools diambil, lalu dihentikan)"))
	cl, err := mcp.Start(nama, sc)
	if err != nil {
		appendMsg(ui.Red("✗ Gagal menjalankan: " + err.Error()))
		appendMsg(ui.Dim("  Periksa: perintah ada di PATH? argumen benar? butuh paket npx/uvx terpasang?"))
		appendMsg("")
		return
	}
	defer cl.Shutdown()
	appendMsg(ui.Green(fmt.Sprintf("✓ Server hidup: %s v%s — %d tools", cl.Server, cl.Version, len(cl.Tools))))
	batas := cl.Tools
	if len(batas) > 12 {
		batas = batas[:12]
	}
	for _, t := range batas {
		d := t.Description
		if rn := []rune(d); len(rn) > 80 {
			d = string(rn[:80]) + "…"
		}
		appendMsg("    • " + ui.Bold(t.Name) + " " + ui.Dim(d))
	}
	if len(cl.Tools) > len(batas) {
		appendMsg(ui.Dim(fmt.Sprintf("    … dan %d tools lain", len(cl.Tools)-len(batas))))
	}
	appendMsg("")
}

// segarkanMCP dipakai setelah mcp.json berubah: memuat ulang server dan
// mendaftarkan tools-nya ke registri yang sedang berjalan.
func segarkanMCP(cfg *config.Config, reg *tools.Registry,
	appendMsg func(string)) []*mcp.Client {
	appendMsg("")
	appendMsg(ui.Dim("  Memuat ulang MCP dari " + configPathMCP(cfg) + " …"))
	clients := loadMCP(cfg, reg)
	if len(clients) == 0 {
		appendMsg(ui.Yellow("  Tidak ada server MCP yang berhasil dijalankan."))
	} else {
		appendMsg(ui.Green(fmt.Sprintf("✓ %d server MCP online", len(clients))))
	}
	appendMsg("")
	return clients
}
