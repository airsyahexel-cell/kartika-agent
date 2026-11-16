// session.go — penyimpanan sesi chat posko: seluruh giliran (user+assistant)
// disimpan otomatis (setiap giliran selesai, saat keluar, dan saat Ctrl+C)
// ke ~/.local/share/kartika/sessions/sesi-<id>.json sehingga bisa dibuka
// ulang dengan `kartika --lanjut`.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"kartika-agent/internal/llm"
	"kartika-agent/internal/ui"
)

// savedSession adalah struktur file sesi tersimpan.
type savedSession struct {
	ID      string        `json:"id"`
	Started string        `json:"started"`
	Saved   string        `json:"saved"`
	Turns   []sessionTurn `json:"turns"`
}

// sessionTurn adalah satu pesan (user/assistant) dalam sesi.
type sessionTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// newSessionID menghasilkan ID sesi dari waktu saat ini.
func newSessionID() string { return time.Now().Format("20060102-150405") }

// sessionDir direktori penyimpanan sesi (XDG data home).
func sessionDir() string {
	base, err := os.UserHomeDir()
	if err != nil || base == "" {
		return "sessions"
	}
	return base + "/.local/share/kartika/sessions"
}

// saveSession menulis sesi secara atomik (tmp + rename); mengembalikan path.
func saveSession(id string, started time.Time, msgs []llm.Message) string {
	dir := sessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	s := savedSession{ID: id, Saved: time.Now().Format(time.RFC3339)}
	if !started.IsZero() {
		s.Started = started.Format(time.RFC3339)
	}
	for _, m := range msgs {
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		s.Turns = append(s.Turns, sessionTurn{Role: m.Role, Content: m.Content})
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return ""
	}
	path := dir + "/sesi-" + id + ".json"
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return ""
	}
	if err := os.Rename(tmp, path); err != nil {
		return ""
	}
	return path
}

// listSessions menampilkan daftar sesi tersimpan (kartika -s / --sesi):
// id, waktu disimpan, jumlah giliran, dan cuplikan pesan pertama operator.
func listSessions() {
	dir := sessionDir()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		fmt.Println("Tidak ada sesi tersimpan.")
		return
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "sesi-") && strings.HasSuffix(e.Name(), ".json") {
			paths = append(paths, dir+"/"+e.Name())
		}
	}
	sort.Strings(paths) // tertua dulu; tampilkan terbaru di bawah agar mudah dibaca
	if len(paths) == 0 {
		fmt.Println("Tidak ada sesi tersimpan.")
		return
	}
	fmt.Println(ui.Bold(ui.Cyan("SESI CHAT TERSIMPAN")))
	for _, p := range paths {
		_, msgs, err := loadSessionFile(p)
		if err != nil {
			continue
		}
		id := strings.TrimPrefix(filepath.Base(p), "sesi-")
		id = strings.TrimSuffix(id, ".json")
		preview := ""
		if len(msgs) > 0 {
			preview = strings.Join(strings.Fields(msgs[0].Content), " ")
			if rn := []rune(preview); len(rn) > 58 {
				preview = string(rn[:58]) + "…"
			}
		}
		saved := ""
		if st, err := os.Stat(p); err == nil {
			saved = st.ModTime().Format("2006-01-02 15:04")
		}
		fmt.Printf("  %s │ %d giliran │ %s │ %s\n",
			ui.Bold(id), len(msgs)/2, ui.Dim(saved), preview)
	}
	fmt.Println()
	fmt.Println(ui.Dim("  Buka ulang: kartika --lanjut <id>  — atau kartika --lanjut untuk sesi terbaru"))
}

func loadLatestSession() (id string, msgs []llm.Message, err error) {
	dir := sessionDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, fmt.Errorf("direktori sesi tidak tersedia")
	}
	var best string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, "sesi-") && strings.HasSuffix(n, ".json") && n > best {
			best = n
		}
	}
	if best == "" {
		return "", nil, fmt.Errorf("tidak ada sesi tersimpan")
	}
	return loadSessionFile(dir + "/" + best)
}

// loadSessionFile membaca satu file sesi menjadi pesan agen.
func loadSessionFile(path string) (string, []llm.Message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	var s savedSession
	if err := json.Unmarshal(data, &s); err != nil {
		return "", nil, fmt.Errorf("file sesi rusak: %w", err)
	}
	var msgs []llm.Message
	for _, t := range s.Turns {
		msgs = append(msgs, llm.Message{Role: t.Role, Content: t.Content})
	}
	return s.ID, msgs, nil
}

// tampilkanRiwayat memutar ulang percakapan tersimpan ke transkrip sehingga
// saat sesi lama dibuka (kartika --lanjut <id>) operator melihat SEMUA
// percakapannya, bukan hanya catatan "N giliran diingat". Tanpa ini memori
// model sudah pulih tetapi layar tampak kosong — membingungkan.
//
// Pesan tool/kosong dilewati; pesan user ditandai "❯" seperti input asli.
func tampilkanRiwayat(msgs []llm.Message, appendMsg func(string)) {
	if appendMsg == nil {
		return
	}
	adaIsi := false
	for _, m := range msgs {
		isi := strings.TrimSpace(m.Content)
		if isi == "" {
			continue
		}
		switch m.Role {
		case "user":
			appendMsg(ui.Bold(ui.Cyan("❯ ")) + ui.Bold(isi))
			adaIsi = true
		case "assistant":
			appendMsg(isi)
			adaIsi = true
		case "system":
			// Instruksi sistem tidak perlu ditampilkan ke operator.
		default:
			// tool / nama lain: tampilkan redup agar jejak eksekusi tetap ada.
			appendMsg(ui.Dim(isi))
			adaIsi = true
		}
	}
	if adaIsi {
		appendMsg("")
		appendMsg(ui.Dim("── akhir riwayat sesi sebelumnya · lanjutkan percakapan di bawah ──"))
	}
}
