package tools

// Modul taktis built-in KARTIKA-AGENT (10 modul inti):
//   1. scan_dir      — pemetaan struktur direktori target (RECON)
//   2. read_file     — pembacaan source code/konfigurasi bernomor baris (VERIFY)
//   3. grep_code     — pencarian pola input/sink di source code (VERIFY)
//   4. http_probe    — probe postur keamanan HTTP/HTTPS (RECON/VERIFY)
//   5. http_request  — request arbitrer + sesi cookie + diff dua identitas (VERIFY)
//   6. run_cmd       — eksekusi perintah shell untuk uji aktif & PoC (VERIFY)
//   7. write_file    — penulisan laporan SITREP & file patch (REPORT)
//   8. skill_list    — daftar pustaka skill metodologi (METODOLOGI)
//   9. skill_read    — baca metodologi satu skill (METODOLOGI)
//  10. skill_search  — cari payload/gerbang bukti lintas pustaka (METODOLOGI)
//
// Seluruh output modul berupa teks polos agar dapat langsung dikonsumsi
// engine sebagai bukti (verification evidence).

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// ---------- utilitas schema & format ----------

func objSchema(props map[string]any, required []string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func pStr(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func pInt(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func pBool(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

func humanSize(n int64) string {
	const kb, mb = 1024, 1024 * 1024
	switch {
	case n >= mb:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(mb))
	case n >= kb:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(kb))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func onOff(b bool) string {
	if b {
		return "AKTIF"
	}
	return "NONAKTIF"
}

// RegisterBuiltins mendaftarkan modul-modul inti ke registri.
func RegisterBuiltins(reg *Registry, writeScope string) {
	reg.Register(scanDirTool())
	reg.Register(readFileTool())
	reg.Register(grepCodeTool())
	reg.Register(httpProbeTool())
	reg.Register(httpRequestTool())
	reg.Register(runCmdTool())
	reg.Register(writeFileTool(writeScope))
	reg.Register(skillListTool())
	reg.Register(skillReadTool())
	reg.Register(skillSearchTool())
}

// ================= 1. scan_dir =================

type scanDirArgs struct {
	Path       string `json:"path"`
	MaxEntries int    `json:"max_entries"`
}

var errEntryLimit = errors.New("batas entri tercapai")

func scanDirTool() *Tool {
	return &Tool{
		Name: "scan_dir",
		Description: "Memetakan struktur direktori target audit: daftar folder dan file beserta ukurannya. " +
			"Gunakan pada FASE RECON untuk mengenali tata letak source code sebelum membaca file tertentu dengan read_file.",
		Schema: objSchema(map[string]any{
			"path":        pStr("Path direktori yang akan dipetakan (default: direktori kerja saat ini)"),
			"max_entries": pInt("Batas jumlah entri yang ditampilkan (default 500)"),
		}, nil),
		Fn: func(raw json.RawMessage) string {
			var a scanDirArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return "ERROR: argumen tidak valid: " + err.Error()
			}
			root := a.Path
			if root == "" {
				root = "."
			}
			abs, err := filepath.Abs(root)
			if err != nil {
				return "ERROR: path tidak valid: " + err.Error()
			}
			info, err := os.Stat(abs)
			if err != nil {
				return "ERROR: tidak dapat mengakses '" + a.Path + "': " + err.Error()
			}
			if !info.IsDir() {
				return "ERROR: '" + a.Path + "' bukan sebuah direktori."
			}
			maxEntries := 500
			if a.MaxEntries > 0 {
				maxEntries = a.MaxEntries
			}
			skip := map[string]bool{
				".git": true, "node_modules": true, "__pycache__": true,
				".venv": true, "venv": true, ".idea": true, ".vscode": true,
				"target": true, "vendor": true, "dist": true, "bin": true,
			}
			var out strings.Builder
			var dirs, files, skipped int
			truncated := false
			fmt.Fprintf(&out, "=== PEMETAAN DIREKTORI ===\nRoot: %s\n", abs)
			_ = filepath.WalkDir(abs, func(p string, d os.DirEntry, werr error) error {
				if werr != nil {
					return nil // entri tidak terbaca dilewati
				}
				name := d.Name()
				if d.IsDir() {
					if p == abs {
						return nil
					}
					if skip[name] {
						skipped++
						return filepath.SkipDir
					}
					dirs++
				} else {
					files++
				}
				if dirs+files > maxEntries {
					truncated = true
					return errEntryLimit
				}
				rel, _ := filepath.Rel(abs, p)
				if d.IsDir() {
					fmt.Fprintf(&out, "[DIR]  %s/\n", rel)
				} else {
					sz := int64(0)
					if fi, e := d.Info(); e == nil {
						sz = fi.Size()
					}
					fmt.Fprintf(&out, "[FILE] %-56s %8s\n", rel, humanSize(sz))
				}
				return nil
			})
			fmt.Fprintf(&out, "Ringkasan: %d folder, %d file", dirs, files)
			if skipped > 0 {
				fmt.Fprintf(&out, " | %d entri dilewati (.git, node_modules, dsb.)", skipped)
			}
			if truncated {
				fmt.Fprintf(&out, " | (DIPOTONG: batas %d entri — perkecil cakupan atau naikkan max_entries)", maxEntries)
			}
			out.WriteString("\n")
			return out.String()
		},
	}
}

// ================= 2. read_file =================

type readFileArgs struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	MaxBytes  int    `json:"max_bytes"`
}

func readFileTool() *Tool {
	return &Tool{
		Name: "read_file",
		Description: "Membaca isi file source code / konfigurasi dengan nomor baris agar setiap temuan dapat dikutip " +
			"sebagai bukti (path + nomor baris). Untuk file besar gunakan start_line dan end_line untuk membaca segmen tertentu.",
		Schema: objSchema(map[string]any{
			"path":       pStr("Path file yang akan dibaca"),
			"start_line": pInt("Baris awal segmen (default 1)"),
			"end_line":   pInt("Baris akhir segmen (default: maksimum yang muat)"),
			"max_bytes":  pInt("Batas byte yang ditampilkan (default 65536)"),
		}, []string{"path"}),
		Fn: func(raw json.RawMessage) string {
			var a readFileArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return "ERROR: argumen tidak valid: " + err.Error()
			}
			if a.Path == "" {
				return "ERROR: parameter 'path' wajib diisi."
			}
			data, err := os.ReadFile(a.Path)
			if err != nil {
				return "ERROR: gagal membaca '" + a.Path + "': " + err.Error()
			}
			probe := data
			if len(probe) > 512 {
				probe = probe[:512]
			}
			if bytes.IndexByte(probe, 0) >= 0 {
				return fmt.Sprintf("=== FILE BINER ===\nPath: %s | Ukuran: %s\nKonten tidak dapat ditampilkan sebagai teks.",
					a.Path, humanSize(int64(len(data))))
			}
			if len(data) == 0 {
				return fmt.Sprintf("=== ISI FILE: %s ===\nFILE KOSONG (0 byte).", a.Path)
			}
			full := strings.ReplaceAll(string(data), "\r\n", "\n")
			lines := strings.Split(full, "\n")
			total := len(lines)
			maxBytes := 65536
			if a.MaxBytes > 0 {
				maxBytes = a.MaxBytes
			}
			start := a.StartLine
			if start < 1 {
				start = 1
			}
			if start > total {
				start = total
			}
			end := a.EndLine
			if end < start {
				end = start + 399
			}
			if end > total {
				end = total
			}
			var out strings.Builder
			fmt.Fprintf(&out, "=== ISI FILE: %s (total %d baris, %s) ===\n", a.Path, total, humanSize(int64(len(data))))
			shown := 0
			cutAt := 0
			for i := start; i <= end; i++ {
				l := lines[i-1]
				if shown+len(l)+8 > maxBytes {
					cutAt = i
					break
				}
				fmt.Fprintf(&out, "%5d  %s\n", i, l)
				shown += len(l) + 8
			}
			if cutAt > 0 {
				fmt.Fprintf(&out, "(DIPOTONG pada baris %d: batas %d byte — lanjutkan dengan start_line=%d)\n", cutAt, maxBytes, cutAt)
			} else if end < total {
				fmt.Fprintf(&out, "(Menampilkan baris %d-%d dari %d total — gunakan start_line/end_line untuk segmen lain)\n", start, end, total)
			}
			return out.String()
		},
	}
}

// ================= 3. http_probe =================

type httpProbeArgs struct {
	URL             string `json:"url"`
	Method          string `json:"method"`
	TimeoutSec      int    `json:"timeout_sec"`
	MaxBodyBytes    int    `json:"max_body_bytes"`
	FollowRedirects bool   `json:"follow_redirects"`
	VerifyTLS       *bool  `json:"verify_tls"`
}

var (
	rxInsecureAction = regexp.MustCompile(`(?i)action\s*=\s*["']\s*http://`)
	rxPasswordInput  = regexp.MustCompile(`(?i)type\s*=\s*["']?password`)
)

func httpProbeTool() *Tool {
	return &Tool{
		Name: "http_probe",
		Description: "Probe HTTP/HTTPS pada sebuah endpoint untuk audit postur keamanan. Mengembalikan: status & waktu respons, " +
			"rantai redirect, seluruh header respons, atribut cookie (HttpOnly/Secure/SameSite), banner teknologi (Server/X-Powered-By), " +
			"checklist security headers (HSTS, CSP, X-Frame-Options, X-Content-Type-Options, Referrer-Policy, Permissions-Policy), " +
			"analisis body (jumlah form, form action cleartext http://, input password), " +
			"serta DAFTAR ASET & TAUTAN yang direferensikan halaman (script .js, stylesheet, iframe, link internal) — " +
			"telusuri setiap entri dengan http_probe berikutnya untuk pemetaan permukaan target yang menyeluruh. " +
			"Gunakan output ini sebagai bukti verifikasi temuan postur keamanan.",
		Schema: objSchema(map[string]any{
			"url":              pStr("URL target lengkap termasuk skema, contoh: http://target.local/login.aspx"),
			"method":           map[string]any{"type": "string", "description": "Metode HTTP (default GET)", "enum": []string{"GET", "HEAD", "OPTIONS", "POST"}},
			"timeout_sec":      pInt("Timeout per hop dalam detik (default 15)"),
			"max_body_bytes":   pInt("Batas byte body yang diambil per hop (default 16384)"),
			"follow_redirects": pBool("Ikuti rantai redirect hingga 5 hop (default false — redirect hanya dilaporkan)"),
			"verify_tls":       pBool("Verifikasi keabsahan sertifikat TLS (default false untuk audit internal / self-signed)"),
		}, []string{"url"}),
		Fn: func(raw json.RawMessage) string { return runProbe(raw) },
	}
}

func runProbe(raw json.RawMessage) string {
	var a httpProbeArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "ERROR: argumen tidak valid: " + err.Error()
	}
	if a.URL == "" {
		return "ERROR: parameter 'url' wajib diisi."
	}
	lowerURL := strings.ToLower(a.URL)
	if !strings.HasPrefix(lowerURL, "http://") && !strings.HasPrefix(lowerURL, "https://") {
		return "ERROR: URL harus diawali http:// atau https://"
	}
	method := strings.ToUpper(strings.TrimSpace(a.Method))
	if method == "" {
		method = "GET"
	}
	timeout := 15 * time.Second
	if a.TimeoutSec > 0 {
		timeout = time.Duration(a.TimeoutSec) * time.Second
	}
	maxBody := 16384
	if a.MaxBodyBytes > 0 {
		maxBody = a.MaxBodyBytes
	}
	verify := false
	if a.VerifyTLS != nil {
		verify = *a.VerifyTLS
	}

	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: !verify},
			Proxy:           http.ProxyFromEnvironment,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // redirect dilaporkan, bukan diikuti diam-diam
		},
	}

	var out strings.Builder
	fmt.Fprintf(&out, "=== HTTP PROBE ===\nTarget : %s\nMetode : %s\nTLS    : ", a.URL, method)
	if strings.HasPrefix(lowerURL, "https://") {
		fmt.Fprintf(&out, "digunakan (verifikasi sertifikat: %s)\n", onOff(verify))
	} else {
		out.WriteString("TIDAK DIGUNAKAN — transmisi berjalan di jalur cleartext\n")
	}

	current := a.URL
	for hop := 1; hop <= 5; hop++ {
		req, err := http.NewRequest(method, current, nil)
		if err != nil {
			fmt.Fprintf(&out, "[HOP %d] URL tidak valid: %v\n", hop, err)
			break
		}
		req.Header.Set("User-Agent", "KARTIKA-AGENT/1.0 (http_probe)")
		start := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			fmt.Fprintf(&out, "[HOP %d] KONEKSI GAGAL: %v\n", hop, err)
			break
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, int64(maxBody)))
		resp.Body.Close()
		elapsed := time.Since(start)

		fmt.Fprintf(&out, "\n[HOP %d] %s %s\n", hop, method, current)
		fmt.Fprintf(&out, "Status : %s (%s) | Waktu respons: %d ms\n", resp.Status, resp.Proto, elapsed.Milliseconds())
		if cl := resp.Header.Get("Content-Length"); cl != "" {
			fmt.Fprintf(&out, "Body   : %d byte diambil (Content-Length: %s)\n", len(body), cl)
		} else {
			fmt.Fprintf(&out, "Body   : %d byte diambil\n", len(body))
		}
		if resp.TLS != nil {
			fmt.Fprintf(&out, "TLS    : %s | Cipher: %s\n", tlsVersionName(resp.TLS.Version), tls.CipherSuiteName(resp.TLS.CipherSuite))
		}

		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			loc := resp.Header.Get("Location")
			fmt.Fprintf(&out, "Redirect : %s -> %q\n", resp.Status, loc)
			if !a.FollowRedirects {
				out.WriteString("(redirect TIDAK diikuti — set follow_redirects=true untuk menelusuri rantai)\n")
				break
			}
			if loc == "" {
				break
			}
			base, e1 := url.Parse(current)
			ref, e2 := url.Parse(loc)
			if e1 != nil || e2 != nil {
				out.WriteString("(Location tidak valid — rantai dihentikan)\n")
				break
			}
			current = base.ResolveReference(ref).String()
			continue
		}

		out.WriteString("Header Respons:\n")
		keys := make([]string, 0, len(resp.Header))
		for k := range resp.Header {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			for _, v := range resp.Header[k] {
				fmt.Fprintf(&out, "  %s: %s\n", k, v)
			}
		}

		out.WriteString("Checklist Security Headers:\n")
		secHeaders := []struct{ name, why string }{
			{"Strict-Transport-Security", "HSTS — memaksa HTTPS"},
			{"Content-Security-Policy", "CSP — mitigasi XSS/injeksi"},
			{"X-Frame-Options", "anti-clickjacking"},
			{"X-Content-Type-Options", "anti-MIME-sniffing"},
			{"Referrer-Policy", "kontrol kebocoran referrer"},
			{"Permissions-Policy", "kontrol akses API browser"},
		}
		for _, sh := range secHeaders {
			if v := resp.Header.Get(sh.name); v != "" {
				val := v
				if len(val) > 80 {
					val = val[:80] + "…"
				}
				fmt.Fprintf(&out, "  %-26s: ADA (%s)\n", sh.name, val)
			} else {
				fmt.Fprintf(&out, "  %-26s: TIDAK ADA (risiko: %s)\n", sh.name, sh.why)
			}
		}

		if cookies := resp.Header["Set-Cookie"]; len(cookies) > 0 {
			out.WriteString("Analisis Cookie:\n")
			for _, ck := range cookies {
				flags := strings.Split(ck, ";")
				nameVal := strings.TrimSpace(flags[0])
				httpOnly, secure, sameSite := "TIDAK", "TIDAK", "TIDAK DISET"
				for _, f := range flags[1:] {
					lf := strings.ToLower(strings.TrimSpace(f))
					switch {
					case lf == "httponly":
						httpOnly = "YA"
					case lf == "secure":
						secure = "YA"
					case strings.HasPrefix(lf, "samesite"):
						parts := strings.SplitN(lf, "=", 2)
						if len(parts) == 2 && parts[1] != "" {
							sameSite = strings.ToUpper(parts[1])
						} else {
							sameSite = "YA (tanpa nilai)"
						}
					}
				}
				fmt.Fprintf(&out, "  %s | HttpOnly=%s | Secure=%s | SameSite=%s\n", nameVal, httpOnly, secure, sameSite)
			}
		}

		banners := []string{"Server", "X-Powered-By", "X-AspNet-Version", "X-AspNetMvc-Version"}
		var found []string
		for _, b := range banners {
			if v := resp.Header.Get(b); v != "" {
				found = append(found, b+": "+v)
			}
		}
		if len(found) > 0 {
			out.WriteString("Banner Teknologi (sidik jari terbuka):\n")
			for _, f := range found {
				fmt.Fprintf(&out, "  %s\n", f)
			}
		}

		ct := resp.Header.Get("Content-Type")
		if isTextual(ct, body) && len(body) > 0 {
			text := string(body)
			lowerBody := strings.ToLower(text)
			fmt.Fprintf(&out, "Analisis Body (Content-Type: %s):\n", ct)
			fmt.Fprintf(&out, "  <form> ditemukan          : %d\n", strings.Count(lowerBody, "<form"))
			fmt.Fprintf(&out, "  form action ke http://    : %d (pengiriman data via cleartext)\n", len(rxInsecureAction.FindAllString(lowerBody, -1)))
			fmt.Fprintf(&out, "  input password ditemukan  : %d\n", len(rxPasswordInput.FindAllString(lowerBody, -1)))
			writeAssetList(&out, current, text)
			snippet := text
			if len(snippet) > 1500 {
				snippet = snippet[:1500] + "\n…(potongan dipangkas)"
			}
			out.WriteString("Potongan Body:\n")
			out.WriteString(indentBlock(snippet))
		} else {
			fmt.Fprintf(&out, "Body: non-teks/biner (Content-Type: %s) — analisis konten dilewati.\n", ct)
		}
		break
	}
	return out.String()
}

// maxAssetList membatasi jumlah aset/tautan yang dicetak per respons.
const maxAssetList = 40

// rxAssetRef menangkap atribut src/href dari HTML (script, link, a, iframe, img).
var rxAssetRef = regexp.MustCompile(`(?i)(?:\bsrc\b|\bhref\b)\s*=\s*["']([^"']+)["']`)

// writeAssetList mengenumerasi seluruh aset & tautan yang direferensikan body
// (file .js, stylesheet, iframe, link internal/eksternal) — bahan baku trace
// menyeluruh oleh agen: setiap entri dapat langsung di-probe pada langkah berikutnya.
func writeAssetList(out *strings.Builder, pageURL, text string) {
	base, err := url.Parse(pageURL)
	if err != nil {
		return
	}
	out.WriteString("Aset & Tautan Terdeteksi (telusuri dengan http_probe untuk trace menyeluruh):\n")
	seen := map[string]bool{}
	n := 0
	for _, m := range rxAssetRef.FindAllStringSubmatch(text, -1) {
		rawRef := strings.TrimSpace(m[1])
		if rawRef == "" || strings.HasPrefix(rawRef, "#") {
			continue
		}
		lower := strings.ToLower(rawRef)
		if strings.HasPrefix(lower, "javascript:") || strings.HasPrefix(lower, "data:") ||
			strings.HasPrefix(lower, "mailto:") || strings.HasPrefix(lower, "tel:") {
			continue
		}
		ref, err := url.Parse(rawRef)
		if err != nil {
			continue
		}
		abs := base.ResolveReference(ref)
		if abs.Scheme != "http" && abs.Scheme != "https" {
			continue
		}
		key := abs.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		kind := "link"
		p := strings.ToLower(abs.Path)
		switch {
		case strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".mjs"):
			kind = "script"
		case strings.HasSuffix(p, ".css"):
			kind = "style"
		case strings.HasSuffix(p, ".json"):
			kind = "json"
		case strings.HasSuffix(p, ".jpg") || strings.HasSuffix(p, ".png") ||
			strings.HasSuffix(p, ".gif") || strings.HasSuffix(p, ".svg") ||
			strings.HasSuffix(p, ".ico") || strings.HasSuffix(p, ".webp"):
			kind = "img"
		case strings.HasSuffix(p, ".txt") || strings.HasSuffix(p, ".xml") ||
			strings.HasSuffix(p, ".config") || strings.HasSuffix(p, ".asax") ||
			strings.HasSuffix(p, ".ashx") || strings.HasSuffix(p, ".asmx"):
			kind = "sumber"
		}
		fmt.Fprintf(out, "  [%s] %s\n", kind, key)
		n++
		if n >= maxAssetList {
			out.WriteString("  …(daftar dipangkas — probe aset ini dulu, lalu ulangi dengan max_body_bytes lebih besar)\n")
			break
		}
	}
	if n == 0 {
		out.WriteString("  (tidak ada aset/tautan pada body yang diambil — naikkan max_body_bytes)\n")
	}
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return fmt.Sprintf("0x%04x", v)
	}
}

func isTextual(ct string, body []byte) bool {
	l := strings.ToLower(ct)
	if strings.Contains(l, "text/") || strings.Contains(l, "html") ||
		strings.Contains(l, "xml") || strings.Contains(l, "json") ||
		strings.Contains(l, "javascript") {
		return true
	}
	if ct == "" && len(body) > 0 {
		sniff := body
		if len(sniff) > 512 {
			sniff = sniff[:512]
		}
		s := strings.ToLower(http.DetectContentType(sniff))
		return strings.HasPrefix(s, "text/") || strings.Contains(s, "html") || strings.Contains(s, "json") || strings.Contains(s, "xml")
	}
	return false
}

func indentBlock(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("  | ")
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String()
}

// ================= 5. run_cmd =================

type runCmdArgs struct {
	Command    string `json:"command"`
	TimeoutSec int    `json:"timeout_sec"`
	Workdir    string `json:"workdir"`
}

// runCmdOutputLimit membatasi output perintah agar tidak membanjiri konteks engine.
const runCmdOutputLimit = 64 * 1024

func runCmdTool() *Tool {
	return &Tool{
		Name: "run_cmd",
		Description: "Menjalankan SATU perintah shell di mesin operator lalu mengembalikan stdout+stderr, kode keluar, dan durasi. " +
			"Dipakai untuk verifikasi aktif yang tidak tercakup modul lain: recon jaringan (nmap, dig, host, ping, ss, arp), " +
			"uji endpoint/TLS (curl, wget, openssl s_client), triase kode (grep, rg, find, wc), dan alat audit lain yang terpasang. " +
			"HANYA untuk target yang diizinkan operator. Satu perintah per pemanggilan — rantai dengan '&&' bila perlu.",
		Schema: objSchema(map[string]any{
			"command":     pStr("Perintah shell lengkap, contoh: nmap -sV -p 22,80,443 target.local || curl -sk https://target.local/robots.txt"),
			"timeout_sec": pInt("Batas waktu eksekusi detik (default 120, maksimum 600)"),
			"workdir":     pStr("Direktori kerja eksekusi (default: direktori saat ini)"),
		}, []string{"command"}),
		Fn: func(raw json.RawMessage) string { return runCmd(raw) },
	}
}

func runCmd(raw json.RawMessage) string {
	var a runCmdArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "ERROR: argumen tidak valid: " + err.Error()
	}
	cmdline := strings.TrimSpace(a.Command)
	if cmdline == "" {
		return "ERROR: parameter 'command' wajib diisi."
	}
	timeout := 120 * time.Second
	if a.TimeoutSec > 0 {
		timeout = time.Duration(a.TimeoutSec) * time.Second
		if timeout > 600*time.Second {
			timeout = 600 * time.Second
		}
	}
	wd := a.Workdir
	if wd != "" {
		abs, err := filepath.Abs(wd)
		if err != nil {
			return "ERROR: workdir tidak valid: " + err.Error()
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			return "ERROR: workdir bukan direktori yang ada: " + wd
		}
		wd = abs
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", cmdline)
	} else {
		cmd = exec.Command("bash", "-c", cmdline)
	}
	if wd != "" {
		cmd.Dir = wd
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return "ERROR: gagal memulai perintah: " + err.Error()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timedOut := false
	var werr error
	select {
	case werr = <-done:
	case <-time.After(timeout):
		timedOut = true
		_ = cmd.Process.Kill()
		werr = <-done
	}

	out := buf.String()
	out = strings.TrimRight(out, "\n")
	if len(out) > runCmdOutputLimit {
		out = out[:runCmdOutputLimit] + "\n…(DIPOTONG: batas 64 KB)"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "=== RUN CMD ===\nPerintah : %s\n", cmdline)
	if wd != "" {
		fmt.Fprintf(&b, "Workdir  : %s\n", wd)
	}
	dur := time.Since(start).Round(time.Millisecond)
	if timedOut {
		fmt.Fprintf(&b, "Status   : TIMEOUT setelah %s — proses dihentikan paksa (naikkan timeout_sec bila perlu)\n", dur)
	} else if werr != nil {
		if ee, ok := werr.(*exec.ExitError); ok {
			fmt.Fprintf(&b, "Status   : Exit %d | Durasi: %s\n", ee.ExitCode(), dur)
		} else {
			return "ERROR: " + werr.Error()
		}
	} else {
		fmt.Fprintf(&b, "Status   : Exit 0 (sukses) | Durasi: %s\n", dur)
	}
	if out == "" {
		b.WriteString("Output   : (kosong)\n")
	} else {
		fmt.Fprintf(&b, "Output (%d byte):\n%s\n", len(out), out)
	}
	return b.String()
}

// ================= 6. write_file =================

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func writeFileTool(scope string) *Tool {
	return &Tool{
		Name: "write_file",
		Description: "Menulis file teks: laporan SITREP Markdown, file patch/rekomendasi konfigurasi hardening, atau catatan misi. " +
			"Penulisan dibatasi di dalam cakupan direktori kerja (atur KARTIKA_WRITE_SCOPE untuk memperluas).",
		Schema: objSchema(map[string]any{
			"path":    pStr("Path tujuan file, contoh: sitrep/sitrep-login-aspx.md"),
			"content": pStr("Konten lengkap yang akan ditulis (UTF-8 / Markdown)"),
		}, []string{"path", "content"}),
		Fn: func(raw json.RawMessage) string {
			var a writeFileArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return "ERROR: argumen tidak valid: " + err.Error()
			}
			if a.Path == "" {
				return "ERROR: parameter 'path' wajib diisi."
			}
			if a.Content == "" {
				return "ERROR: parameter 'content' kosong — tulis konten laporan secara lengkap."
			}
			scopeAbs, err := filepath.Abs(scope)
			if err != nil {
				return "ERROR: cakupan kerja tidak valid: " + err.Error()
			}
			var target string
			if filepath.IsAbs(a.Path) {
				target = filepath.Clean(a.Path)
			} else {
				target = filepath.Join(scopeAbs, a.Path)
			}
			rel, err := filepath.Rel(scopeAbs, target)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Sprintf("ERROR: penulisan '%s' berada di luar cakupan kerja '%s' (dilarang). "+
					"Gunakan path relatif, atau atur KARTIKA_WRITE_SCOPE untuk memperluas cakupan.", a.Path, scope)
			}
			if dir := filepath.Dir(target); dir != "." {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return "ERROR: gagal membuat direktori tujuan: " + err.Error()
				}
			}
			if err := os.WriteFile(target, []byte(a.Content), 0o644); err != nil {
				return "ERROR: gagal menulis file: " + err.Error()
			}
			return fmt.Sprintf("=== FILE DITULIS ===\nPath   : %s\nUkuran : %s\nStatus : OK", target, humanSize(int64(len(a.Content))))
		},
	}
}
