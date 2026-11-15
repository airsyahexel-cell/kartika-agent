package tools

// Modul grep_code — pencarian pola di source code target (analisa statis):
// mencari field input, sink berbahaya (query SQL, echo, exec, include), API
// endpoint, dan pola lain lintas file (PHP/JS/Java/Python/dll.) dengan
// nomor baris — dasar bagi engine untuk melacak input → sink dan
// membangun payload dari konteks kode yang aktual.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type grepCodeArgs struct {
	Path       string `json:"path"`
	Pattern    string `json:"pattern"`
	Glob       string `json:"glob"`
	Regex      bool   `json:"regex"`
	MaxResults int    `json:"max_results"`
	MaxDepth   int    `json:"max_depth"`
}

func grepCodeTool() *Tool {
	return &Tool{
		Name: "grep_code",
		Description: "Mencari pola (substring atau regex) di source code target dan mengembalikan file:baris:teks. " +
			"Gunakan untuk analisa statis: melacak field input (\"$_GET\", \"$_POST\", \"req.query\", \"request.form\"), " +
			"sink berbahaya (\"query(\", \"echo\", \"exec\", \"system\", \"include\", \"eval\", \"innerHTML\", \"fetch(\"), " +
			"endpoint API, dan koneksi field→sink sebelum membangun payload. Lebih murah daripada membaca file utuh.",
		Schema: objSchema(map[string]any{
			"path":        pStr("Path direktori/file yang dicari (default: direktori kerja)"),
			"pattern":     pStr("Pola yang dicari — substring biasa, atau regex bila regex=true"),
			"glob":        pStr("Filter nama file, mis. \"*.php\" atau \"*.js\" (opsional)"),
			"regex":       pBool("Perlakukan pattern sebagai regex Go (default false = substring)"),
			"max_results": pInt("Batas jumlah baris hasil (default 60, maks 300)"),
			"max_depth":   pInt("Kedalaman folder yang dijelajahi (default 10)"),
		}, []string{"pattern"}),
		Fn: func(raw json.RawMessage) string {
			var a grepCodeArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return "ERROR: argumen tidak valid: " + err.Error()
			}
			if strings.TrimSpace(a.Pattern) == "" {
				return "ERROR: pattern kosong."
			}
			root := a.Path
			if root == "" {
				root = "."
			}
			abs, err := filepath.Abs(root)
			if err != nil {
				return "ERROR: path tidak valid."
			}
			info, err := os.Stat(abs)
			if err != nil {
				return "ERROR: tidak dapat mengakses '" + a.Path + "': " + err.Error()
			}
			max := a.MaxResults
			if max <= 0 {
				max = 60
			}
			if max > 300 {
				max = 300
			}
			depth := a.MaxDepth
			if depth <= 0 {
				depth = 10
			}
			if depth > 20 {
				depth = 20
			}
			var matcher func(string) bool
			if a.Regex {
				re, rerr := regexp.Compile(a.Pattern)
				if rerr != nil {
					return "ERROR: regex tidak valid: " + rerr.Error()
				}
				matcher = re.MatchString
			} else {
				needle := strings.ToLower(a.Pattern)
				matcher = func(s string) bool { return strings.Contains(strings.ToLower(s), needle) }
			}

			var files []string
			if info.IsDir() {
				err = filepath.WalkDir(abs, func(p string, d os.DirEntry, werr error) error {
					if werr != nil {
						return nil
					}
					rel, _ := filepath.Rel(abs, p)
					if d.IsDir() {
						if strings.Count(rel, string(filepath.Separator)) >= depth {
							return filepath.SkipDir
						}
						name := d.Name()
						// Lewati direktori dependensi/vendor yang terlalu besar.
						switch name {
						case ".git", "node_modules", "vendor", "__pycache__", ".svn", "dist", "build":
							return filepath.SkipDir
						}
						return nil
					}
					if a.Glob != "" {
						ok, _ := filepath.Match(a.Glob, d.Name())
						if !ok {
							return nil
						}
					}
					fi, _ := d.Info()
					if fi != nil && fi.Size() > 2*1024*1024 {
						return nil // lewati file raksasa (mungkin binary/build)
					}
					files = append(files, p)
					return nil
				})
				if err != nil {
					return "ERROR: pemindaian gagal: " + err.Error()
				}
			} else {
				files = []string{abs}
			}
			sort.Strings(files)

			var b strings.Builder
			hits := 0
			scanned := 0
			for _, f := range files {
				if hits >= max {
					break
				}
				data, rerr := os.ReadFile(f)
				if rerr != nil {
					continue
				}
				// buang file binary sederhana
				if strings.ContainsRune(string(data[:min(len(data), 1024)]), 0) {
					continue
				}
				scanned++
				for i, ln := range strings.Split(string(data), "\n") {
					if hits >= max {
						break
					}
					if !matcher(ln) {
						continue
					}
					t := strings.TrimSpace(ln)
					if rn := []rune(t); len(rn) > 240 {
						t = string(rn[:240]) + "…"
					}
					rel := f
					if r2, e2 := filepath.Rel(abs, f); e2 == nil {
						rel = r2
					}
					fmt.Fprintf(&b, "%s:%d: %s\n", rel, i+1, t)
					hits++
				}
			}
			if hits == 0 {
				return fmt.Sprintf("Pola %q tidak ditemukan (%d file diperiksa, glob=%s).", a.Pattern, scanned, a.Glob)
			}
			head := fmt.Sprintf("=== GREP CODE %q === %d baris cocok (%d file, glob=%s, depth=%d)\n", a.Pattern, hits, scanned, a.Glob, depth)
			if hits >= max {
				head += fmt.Sprintf("(terpotong pada %d hasil — perkecil pola atau naikkan max_results)\n", max)
			}
			return head + b.String()
		},
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
