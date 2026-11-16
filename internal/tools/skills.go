package tools

// Modul pustaka SKILL (metodologi bug bounty & red team):
//   1. skill_list  — daftar seluruh skill tersedia (RECON/METODOLOGI)
//   2. skill_read  — baca isi satu SKILL.md penuh (METODOLOGI)
//   3. skill_search — cari kata kunci di seluruh pustaka skill (PAYLOAD/VALIDASI)
//
// Pustaka default: ~/.local/share/kartika/skills (clone repositori
// murrtada/bug-bounty-agent-skills — format Agent Skills standar:
// <dir>/<nama-skill>/SKILL.md berfrontmatter YAML name+description).
// Path dapat diarahkan lewat KARTIKA_SKILLS_DIR.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SkillEntry adalah satu skill terdaftar dalam pustaka.
type SkillEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
}

var (
	skillCache     []SkillEntry
	skillCacheOnce bool
)

// frontmatterRegex mengambil pasangan name:/description: dari YAML
// frontmatter SKILL.md (baris tunggal setelah '---' pembuka).
var (
	fmNameRx = regexp.MustCompile(`(?m)^name:\s*(.+)$`)
	fmDescRx = regexp.MustCompile(`(?m)^description:\s*(.+)$`)
)

// SkillsRoot mengembalikan direktori pustaka skill.
func SkillsRoot() string {
	if d := os.Getenv("KARTIKA_SKILLS_DIR"); d != "" {
		return d
	}
	base, err := os.UserHomeDir()
	if err != nil || base == "" {
		return "skills"
	}
	return base + "/.local/share/kartika/skills"
}

// loadSkills memindai pustaka skill — menemukan seluruh SKILL.md dalam
// kedalaman 4 level dari root (mendukung layout datar <root>/<nama>/SKILL.md
// maupun clone repositori <root>/<repo>/skills/<nama>/SKILL.md).
func loadSkills() []SkillEntry {
	if skillCacheOnce {
		return skillCache
	}
	skillCacheOnce = true
	root := SkillsRoot()
	seen := map[string]bool{}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// batasi kedalaman relatif dari root agar pemindaian tetap murah
			rel, rerr := filepath.Rel(root, path)
			if rerr == nil && strings.Count(rel, string(filepath.Separator)) > 3 {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "SKILL.md" {
			return nil
		}
		dir := filepath.Base(filepath.Dir(path))
		if seen[dir] {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		name, desc := dir, ""
		if m := fmNameRx.FindStringSubmatch(string(data)); m != nil {
			name = strings.TrimSpace(m[1])
		}
		if m := fmDescRx.FindStringSubmatch(string(data)); m != nil {
			desc = strings.TrimSpace(m[1])
		}
		seen[dir] = true
		skillCache = append(skillCache, SkillEntry{Name: name, Description: collapseSpace(desc), Path: path})
		return nil
	})
	sort.Slice(skillCache, func(i, j int) bool { return skillCache[i].Name < skillCache[j].Name })
	return skillCache
}

// collapseSpace merapikan spasi beruntun dalam deskripsi frontmatter.
func collapseSpace(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

// findSkill mencari skill berdasarkan nama (persis, lalu prefiks/kandungan).
func findSkill(query string) (SkillEntry, bool) {
	skills := loadSkills()
	q := strings.ToLower(strings.TrimSpace(query))
	q = strings.TrimSuffix(q, ".md")
	if idx := strings.LastIndex(q, "/"); idx >= 0 {
		q = q[idx+1:]
	}
	for _, s := range skills {
		if strings.EqualFold(s.Name, q) {
			return s, true
		}
	}
	for _, s := range skills {
		if strings.HasPrefix(strings.ToLower(s.Name), q) {
			return s, true
		}
	}
	for _, s := range skills {
		if strings.Contains(strings.ToLower(s.Name), q) {
			return s, true
		}
	}
	return SkillEntry{}, false
}

// skillListTool mendaftarkan modul skill_list.
func skillListTool() *Tool {
	return &Tool{
		Name: "skill_list",
		Description: "Menampilkan pustaka SKILL metodologi (bug bounty & red team): nama + deskripsi + path. " +
			"Gunakan di awal penugasan untuk memilih skill yang relevan dengan kelas kerentanan target, " +
			"lalu baca isinya dengan skill_read.",
		Schema: objSchema(nil, nil),
		Fn: func(raw json.RawMessage) string {
			skills := loadSkills()
			if len(skills) == 0 {
				return fmt.Sprintf("Pustaka skill kosong — direktori: %s (isi dengan clone murrtada/bug-bounty-agent-skills)", SkillsRoot())
			}
			var b strings.Builder
			b.WriteString(fmt.Sprintf("=== PUSTAKA SKILL === %d skill @ %s\n", len(skills), SkillsRoot()))
			for _, s := range skills {
				desc := s.Description
				if rn := []rune(desc); len(rn) > 180 {
					desc = string(rn[:180]) + "…"
				}
				b.WriteString(fmt.Sprintf("- %s — %s\n", s.Name, desc))
			}
			b.WriteString("Baca metodologi lengkap satu skill dengan skill_read(name).")
			return b.String()
		},
	}
}

// skillReadTool mendaftarkan modul skill_read.
func skillReadTool() *Tool {
	return &Tool{
		Name: "skill_read",
		Description: "Membaca metodologi penuh satu skill (SKILL.md): gerbang validasi, tabel bypass, payload, " +
			"dan rantai eskalasi untuk kelas kerentanan tertentu (mis. ssrf, xss, idor, jwt, llm). " +
			"Panggil SEBELUM menguji kelas yang belum dikuasai, dan ikuti gerbang buktinya (zero false positive).",
		Schema: objSchema(map[string]any{
			"name":      pStr("Nama skill (mis. hunt-ssrf, bug-bounty, triage-validation)"),
			"max_chars": pInt("Batas karakter yang dikembalikan (default 20000)"),
		}, []string{"name"}),
		Fn: func(raw json.RawMessage) string {
			var a struct {
				Name     string `json:"name"`
				MaxChars int    `json:"max_chars"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return "ERROR: argumen tidak valid: " + err.Error()
			}
			s, ok := findSkill(a.Name)
			if !ok {
				return "ERROR: skill '" + a.Name + "' tidak ditemukan — jalankan skill_list untuk melihat nama yang tersedia."
			}
			data, err := os.ReadFile(s.Path)
			if err != nil {
				return "ERROR: tidak dapat membaca " + s.Path + ": " + err.Error()
			}
			out := string(data)
			max := a.MaxChars
			if max <= 0 || max > 40000 {
				max = 20000
			}
			truncated := ""
			if rn := []rune(out); len(rn) > max {
				out = string(rn[:max])
				truncated = fmt.Sprintf("\n… [TERPOTONG %d karakter — baca lanjutan dengan skill_read(name, max_chars lebih besar)]", len(rn)-max)
			}
			return fmt.Sprintf("=== SKILL: %s ===\n%s%s", s.Name, out, truncated)
		},
	}
}

// skillSearchTool mendaftarkan modul skill_search.
func skillSearchTool() *Tool {
	return &Tool{
		Name: "skill_search",
		Description: "Mencari kata kunci (payload, gerbang validasi, tabel bypass, CVE, header) di seluruh pustaka skill. " +
			"Gunakan saat butuh payload untuk fingerprint teknologi tertentu atau kriteria bukti untuk kelas kerentanan. " +
			"Hasil: nama skill + baris yang cocok, lalu dalami dengan skill_read.",
		Schema: objSchema(map[string]any{
			"query": pStr("Kata kunci pencarian (mis. 'blind ssrf oob', 'jwt alg none', 'prototype pollution lodash')"),
			"limit": pInt("Batas jumlah hasil skill (default 8)"),
		}, []string{"query"}),
		Fn: func(raw json.RawMessage) string {
			var a struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return "ERROR: argumen tidak valid: " + err.Error()
			}
			if strings.TrimSpace(a.Query) == "" {
				return "ERROR: query kosong."
			}
			skills := loadSkills()
			if len(skills) == 0 {
				return "Pustaka skill kosong — direktori: " + SkillsRoot()
			}
			limit := a.Limit
			if limit <= 0 || limit > 20 {
				limit = 8
			}
			needle := strings.ToLower(a.Query)
			terms := strings.Fields(needle)
			var b strings.Builder
			hits := 0
			for _, s := range skills {
				data, err := os.ReadFile(s.Path)
				if err != nil {
					continue
				}
				lines := strings.Split(string(data), "\n")
				var matched []string
				for i, ln := range lines {
					low := strings.ToLower(ln)
					if !strings.Contains(low, needle) && !allTermsIn(low, terms) {
						continue
					}
					t := strings.TrimSpace(ln)
					if t == "" || len(t) < 4 {
						continue
					}
					if rn := []rune(t); len(rn) > 200 {
						t = string(rn[:200]) + "…"
					}
					matched = append(matched, fmt.Sprintf("  L%d: %s", i+1, t))
					if len(matched) >= 6 {
						break
					}
				}
				if len(matched) == 0 {
					continue
				}
				hits++
				b.WriteString(fmt.Sprintf("%s (%s)\n%s\n\n", s.Name, s.Path, strings.Join(matched, "\n")))
				if hits >= limit {
					break
				}
			}
			if hits == 0 {
				return fmt.Sprintf("Tidak ada skill yang memuat %q — coba kata kunci lebih umum.", a.Query)
			}
			return fmt.Sprintf("=== PENCARIAN SKILL %q — %d skill cocok ===\n%s", a.Query, hits, b.String())
		},
	}
}

// allTermsIn melaporkan true bila seluruh term ada di s.
func allTermsIn(s string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(s, t) {
			return false
		}
	}
	return len(terms) > 0
}

// SkillIndex mengembalikan indeks ringkas seluruh skill untuk system prompt:
// satu baris per skill "nama — deskripsi(≤140)". Indeks dibangun dari
// pemindaian pustaka saat itu — skill yang baru ditambahkan/dihapus otomatis
// tercermin setelah RefreshSkills().
func SkillIndex() string {
	skills := loadSkills()
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	for _, s := range skills {
		d := s.Description
		if rn := []rune(d); len(rn) > 140 {
			d = string(rn[:140]) + "…"
		}
		b.WriteString("- " + s.Name + " — " + d + "\n")
	}
	return b.String()
}

// RefreshSkills memaksa pemindaian ulang pustaka skill — dipanggil saat
// penugasan dimulai sehingga skill yang baru ditambahkan ke folder pustaka
// langsung terpakai tanpa konfigurasi ulang.
func RefreshSkills() {
	skillCacheOnce = false
	skillCache = nil
}

// SkillsCount mengembalikan jumlah skill terdaftar (untuk banner status).
func SkillsCount() int { return len(loadSkills()) }

// SkillsAll mengembalikan salinan seluruh skill terdaftar (untuk /skill).
func SkillsAll() []SkillEntry {
	src := loadSkills()
	out := make([]SkillEntry, len(src))
	copy(out, src)
	return out
}

// SkillDoc membaca isi satu skill untuk tampilan /skill <nama>:
// mengembalikan (entry, isi-dokumen, ditemukan).
func SkillDoc(name string, max int) (SkillEntry, string, bool) {
	s, ok := findSkill(name)
	if !ok {
		return SkillEntry{}, "", false
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return s, "", false
	}
	if max <= 0 || max > 20000 {
		max = 8000
	}
	out := string(data)
	if rn := []rune(out); len(rn) > max {
		out = string(rn[:max]) + fmt.Sprintf("\n… [TERPOTONG — %d karakter lagi; engine bisa membaca penuh via modul skill_read]", len(rn)-max)
	}
	return s, out, true
}
