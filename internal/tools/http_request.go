package tools

// Modul http_request — klien HTTP arbitrer dengan SESI (cookie jar)
// persisten antar langkah dan mode DIFF dua request.
//
// Kenapa dibutuhkan: audit broken access control (IDOR/BOLA) dan alur
// multi-identitas memerlukan state HTTP yang bertahan antar pemanggilan
// modul — login sekali dengan sesi "user", lalu semua request berikutnya
// membawa cookie sesi itu. Mode diff membandingkan respons dua request
// (dua identitas, atau dengan/tanpa parameter) dalam SATU panggilan —
// bukti IDOR langsung dari perbedaan konten.
//
// Cookie jar disimpan di memori + dipersistenkan ke
// ~/.local/share/kartika/cookies.json agar bertahan antar sesi kartika.

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type hrSide struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Session string            `json:"session"`
}

type hrArgs struct {
	URL             string            `json:"url"`
	Method          string            `json:"method"`
	Headers         map[string]string `json:"headers"`
	Body            string            `json:"body"`
	Session         string            `json:"session"`
	SaveCookies     *bool             `json:"save_cookies"`
	ClearSession    bool              `json:"clear_session"`
	FollowRedirects bool              `json:"follow_redirects"`
	TimeoutSec      int               `json:"timeout_sec"`
	VerifyTLS       bool              `json:"verify_tls"`
	MaxBodyBytes    int               `json:"max_body_bytes"`
	Action          string            `json:"action"` // "" | "list_sessions" | "clear"
	Diff            *struct {
		A hrSide `json:"a"`
		B hrSide `json:"b"`
	} `json:"diff"`
}

// ---------- cookie jar persisten ----------

type cookieEntry struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Domain  string `json:"domain"`
	Path    string `json:"path"`
	Secure  bool   `json:"secure"`
	Expires string `json:"expires,omitempty"` // RFC3339; kosong = session cookie
}

var (
	jarMu     sync.Mutex
	jars      map[string]map[string]cookieEntry // nama sesi -> kunci -> entri
	jarLoaded bool
)

func jarFile() string {
	base, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, ".local", "share", "kartika", "cookies.json")
}

func loadJars() {
	jarMu.Lock()
	defer jarMu.Unlock()
	if jarLoaded {
		return
	}
	jarLoaded = true
	jars = map[string]map[string]cookieEntry{}
	if data, err := os.ReadFile(jarFile()); err == nil {
		_ = json.Unmarshal(data, &jars)
	}
}

func saveJarsLocked() {
	if f := jarFile(); f != "" {
		_ = os.MkdirAll(filepath.Dir(f), 0o700)
		if data, err := json.MarshalIndent(jars, "", " "); err == nil {
			_ = os.WriteFile(f, data, 0o600)
		}
	}
}

func jarKey(e cookieEntry) string { return strings.ToLower(e.Domain) + "|" + e.Path + "|" + e.Name }

// jarSend mengembalikan cookie sesi yang cocok dengan host/path target.
func jarSend(session, host, reqPath string, isHTTPS bool) []cookieEntry {
	if session == "" || session == "none" || session == "stateless" {
		return nil
	}
	jarMu.Lock()
	defer jarMu.Unlock()
	loadJarsLocked()
	var out []cookieEntry
	now := time.Now()
	for _, e := range jars[session] {
		if e.Expires != "" {
			if t, err := time.Parse(time.RFC3339, e.Expires); err == nil && t.Before(now) {
				continue
			}
		}
		if e.Secure && !isHTTPS {
			continue
		}
		d := strings.ToLower(e.Domain)
		if d != host && !strings.HasSuffix(host, "."+strings.TrimPrefix(d, ".")) {
			continue
		}
		if e.Path != "" && !strings.HasPrefix(reqPath, e.Path) {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// jarReceive menyimpan Set-Cookie dari respons ke sesi.
func jarReceive(session, host, reqPath string, resp *http.Response, save bool) []cookieEntry {
	if session == "" || session == "none" || session == "stateless" {
		return nil
	}
	jarMu.Lock()
	defer jarMu.Unlock()
	loadJarsLocked()
	if jars[session] == nil {
		jars[session] = map[string]cookieEntry{}
	}
	var stored []cookieEntry
	for _, c := range resp.Cookies() {
		if c.Name == "" {
			continue
		}
		e := cookieEntry{Name: c.Name, Value: c.Value, Path: "/", Secure: c.Secure}
		if c.Domain != "" {
			e.Domain = strings.TrimPrefix(strings.ToLower(c.Domain), ".")
		} else {
			e.Domain = strings.ToLower(host)
		}
		if c.Path != "" {
			e.Path = c.Path
		} else {
			// default-path RFC6265: direktori dari path request
			if i := strings.LastIndex(reqPath, "/"); i > 0 {
				e.Path = reqPath[:i+1]
			}
		}
		if !c.Expires.IsZero() {
			e.Expires = c.Expires.UTC().Format(time.RFC3339)
		} else if c.MaxAge > 0 {
			e.Expires = time.Now().Add(time.Duration(c.MaxAge) * time.Second).UTC().Format(time.RFC3339)
		}
		jars[session][jarKey(e)] = e
		stored = append(stored, e)
	}
	if save {
		saveJarsLocked()
	}
	return stored
}

func loadJarsLocked() {
	if jarLoaded {
		return
	}
	jarLoaded = true
	jars = map[string]map[string]cookieEntry{}
	if data, err := os.ReadFile(jarFile()); err == nil {
		_ = json.Unmarshal(data, &jars)
	}
}

func jarClear(session string) {
	jarMu.Lock()
	defer jarMu.Unlock()
	loadJarsLocked()
	delete(jars, session)
	saveJarsLocked()
}

// ---------- eksekusi request ----------

type hrResult struct {
	status      string
	code        int
	elapsedMs   int64
	bodyLen     int
	body        []byte
	header      http.Header
	tlsUsed     bool
	sentCookies []cookieEntry
	setCookies  []cookieEntry
	contentType string
}

func hrDo(side hrSide, timeout, maxBody int, verify, follow, saveCookies bool) (hrResult, string) {
	if strings.TrimSpace(side.URL) == "" {
		return hrResult{}, "URL kosong"
	}
	lower := strings.ToLower(side.URL)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return hrResult{}, "URL harus diawali http:// atau https://"
	}
	method := strings.ToUpper(strings.TrimSpace(side.Method))
	if method == "" {
		method = "GET"
	}
	u, err := url.Parse(side.URL)
	if err != nil {
		return hrResult{}, "URL tidak valid: " + err.Error()
	}
	reqPath := "/"
	if p := u.Path; p != "" {
		reqPath = p
	}
	host := strings.ToLower(u.Hostname())
	if h := u.Port(); h != "" {
		host = host + ":" + h
	}
	isHTTPS := u.Scheme == "https"

	client := &http.Client{
		Timeout: time.Duration(timeout) * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: !verify},
			Proxy:           http.ProxyFromEnvironment,
		},
	}
	if !follow {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	} else {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("berhenti setelah 5 redirect")
			}
			return nil
		}
	}

	var bodyReader io.Reader
	if side.Body != "" {
		bodyReader = strings.NewReader(side.Body)
	}
	req, err := http.NewRequest(method, side.URL, bodyReader)
	if err != nil {
		return hrResult{}, "request tidak valid: " + err.Error()
	}
	for k, v := range side.Headers {
		if strings.EqualFold(k, "Host") {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	if _, ok := req.Header["User-Agent"]; !ok {
		req.Header.Set("User-Agent", "KARTIKA-AGENT/1.0 (http_request)")
	}
	if side.Body != "" {
		if _, ok := req.Header["Content-Type"]; !ok {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}

	// kirim cookie sesi (tidak menimpa header Cookie manual dari engine)
	var sent []cookieEntry
	if _, manual := req.Header["Cookie"]; !manual {
		sent = jarSend(side.Session, host, reqPath, isHTTPS)
		if len(sent) > 0 {
			pairs := make([]string, 0, len(sent))
			for _, e := range sent {
				pairs = append(pairs, e.Name+"="+e.Value)
			}
			req.Header.Set("Cookie", strings.Join(pairs, "; "))
		}
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return hrResult{}, "koneksi gagal: " + err.Error()
	}
	elapsed := time.Since(start)
	body, _ := io.ReadAll(io.LimitReader(resp.Body, int64(maxBody)))
	resp.Body.Close()

	// simpan Set-Cookie ke sesi (termasuk saat redirect diikuti: hop terakhir)
	finalHost, finalPath := host, reqPath
	if follow && resp.Request != nil {
		if ru := resp.Request.URL; ru != nil {
			finalHost = strings.ToLower(ru.Hostname())
			if p := ru.Port(); p != "" {
				finalHost += ":" + p
			}
			if ru.Path != "" {
				finalPath = ru.Path
			}
		}
	}
	set := jarReceive(side.Session, finalHost, finalPath, resp, saveCookies && side.Session != "")

	return hrResult{
		status: resp.Status, code: resp.StatusCode, elapsedMs: elapsed.Milliseconds(),
		bodyLen: len(body), body: body, header: resp.Header,
		tlsUsed: resp.TLS != nil, sentCookies: sent, setCookies: set,
		contentType: resp.Header.Get("Content-Type"),
	}, ""
}

// ---------- util output ----------

func hrHeaderKeys(h http.Header) []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func hrShowCookies(label string, list []cookieEntry, out *strings.Builder) {
	if len(list) == 0 {
		return
	}
	fmt.Fprintf(out, "%s:\n", label)
	for _, e := range list {
		exp := "session"
		if e.Expires != "" {
			exp = e.Expires
		}
		fmt.Fprintf(out, "  %s=%s | domain=%s path=%s secure=%v kadaluarsa=%s\n",
			e.Name, e.Value, e.Domain, e.Path, e.Secure, exp)
	}
}

func hrBodySection(r hrResult, out *strings.Builder) {
	fmt.Fprintf(out, "Body (%d byte diambil, Content-Type: %s):\n", r.bodyLen, r.contentType)
	if r.bodyLen == 0 {
		out.WriteString("  (kosong)\n")
		return
	}
	text := string(r.body)
	if rn := []rune(text); len(rn) > 20000 {
		text = string(rn[:20000]) + "\n…(terpotong)"
	}
	out.WriteString(text + "\n")
}

// ---------- modul ----------

func httpRequestTool() *Tool {
	return &Tool{
		Name: "http_request",
		Description: "Kirim HTTP request ARBITRER (metode/headers/body apa pun) dengan SESI cookie persisten antar panggilan, dan bandingkan dua request sekaligus (mode diff). " +
			"Alur multi-identitas: (1) login via POST dengan session=\"user\" — cookie sesi tersimpan; (2) semua panggilan berikutnya dengan session=\"user\" otomatis membawa cookie itu; " +
			"(3) ulangi login dengan session=\"admin\" untuk identitas kedua; (4) gunakan mode diff untuk membandingkan respons dua sesi pada endpoint sama — perbedaan konten = bukti broken access control/IDOR. " +
			"Untuk uji payload: bawa payload lewat body/headers tanpa sesi. Cookie jar bertahan antar langkah dan antar sesi kartika (disimpan di disk). " +
			"Gunakan action=\"list_sessions\" untuk melihat isi jar.",
		Schema: objSchema(map[string]any{
			"url":              pStr("URL lengkap — wajib bila mode request tunggal"),
			"method":           pStr("Metode HTTP: GET/POST/PUT/DELETE/PATCH/HEAD/OPTIONS (default GET)"),
			"headers":          map[string]any{"type": "object", "description": "Header kustom, mis. {\"Content-Type\":\"application/json\",\"Authorization\":\"Bearer ...\"}. Kunci Host memaksa header Host."},
			"body":             pStr("Body request (string; Content-Type default form-urlencoded bila tidak diset)"),
			"session":          pStr("Nama sesi cookie jar: \"user\", \"admin\", \"anon\"… Cookie dari respons disimpan dan dikirim ulang otomatis di panggilan berikutnya. Kosong = stateless."),
			"save_cookies":     pBool("Simpan Set-Cookie ke jar sesi (default true bila session diset)"),
			"clear_session":    pBool("Hapus jar sesi bernama 'session' (logout bersih)"),
			"action":           map[string]any{"type": "string", "enum": []string{"", "list_sessions", "clear"}, "description": "list_sessions: tampilkan semua sesi & cookie; clear: hapus sesi 'session'"},
			"follow_redirects": pBool("Ikuti redirect hingga 5 hop (default false — redirect dilaporkan)"),
			"timeout_sec":      pInt("Timeout detik (default 15)"),
			"verify_tls":       pBool("Verifikasi sertifikat TLS (default false)"),
			"max_body_bytes":   pInt("Batas body yang diambil (default 16384)"),
			"diff": map[string]any{
				"type": "object",
				"description": "Mode pembanding: {\"a\":{...request...},\"b\":{...request...}} — jalankan dua request dan laporkan perbedaan status/header/body. " +
					"Bukti IDOR: sisi A session=\"user\" url=...?id=123, sisi B session=\"user2\" url sama — body berbeda = akses lintas akun terbuka.",
			},
		}, nil),
		Fn: func(raw json.RawMessage) string { return runHTTPRequest(raw) },
	}
}

func runHTTPRequest(raw json.RawMessage) string {
	var a hrArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "ERROR: argumen tidak valid: " + err.Error()
	}
	loadJars()

	switch a.Action {
	case "list_sessions":
		jarMu.Lock()
		defer jarMu.Unlock()
		loadJarsLocked()
		if len(jars) == 0 {
			return "Belum ada sesi cookie tersimpan."
		}
		names := make([]string, 0, len(jars))
		for n := range jars {
			names = append(names, n)
		}
		sort.Strings(names)
		var b strings.Builder
		b.WriteString("=== SESI COOKIE ===\n")
		for _, n := range names {
			fmt.Fprintf(&b, "Sesi %q (%d cookie):\n", n, len(jars[n]))
			list := make([]cookieEntry, 0, len(jars[n]))
			for _, e := range jars[n] {
				list = append(list, e)
			}
			sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
			for _, e := range list {
				fmt.Fprintf(&b, "  %s=%s | domain=%s path=%s secure=%v kadaluarsa=%s\n", e.Name, e.Value, e.Domain, e.Path, e.Secure, orDefault(e.Expires, "session"))
			}
		}
		return b.String()
	case "clear":
		if a.Session == "" {
			return "ERROR: clear memerlukan parameter 'session'."
		}
		jarClear(a.Session)
		return fmt.Sprintf("Sesi %q dihapus.", a.Session)
	}

	timeout := 15
	if a.TimeoutSec > 0 {
		timeout = a.TimeoutSec
	}
	maxBody := 16384
	if a.MaxBodyBytes > 0 {
		maxBody = a.MaxBodyBytes
	}
	save := true
	if a.SaveCookies != nil {
		save = *a.SaveCookies
	}
	session := a.Session
	if session == "" && a.ClearSession {
		return "ERROR: clear_session memerlukan 'session'."
	}
	if session != "" && a.ClearSession {
		jarClear(session)
	}

	// ---- mode diff dua request ----
	if a.Diff != nil {
		var out strings.Builder
		out.WriteString("=== HTTP REQUEST DIFF ===\n")
		resA, errA := hrDo(a.Diff.A, timeout, maxBody, a.VerifyTLS, false, save)
		resB, errB := hrDo(a.Diff.B, timeout, maxBody, a.VerifyTLS, false, save)
		side := func(tag string, s hrSide, r hrResult, e string) {
			fmt.Fprintf(&out, "%s: %s %s", tag, strings.ToUpper(orDefault(s.Method, "GET")), s.URL)
			if s.Session != "" {
				fmt.Fprintf(&out, " (sesi %q)", s.Session)
			}
			if e != "" {
				fmt.Fprintf(&out, " -> GAGAL: %s\n", e)
				return
			}
			fmt.Fprintf(&out, " -> %s | %d ms | %d byte\n", r.status, r.elapsedMs, r.bodyLen)
			hrShowCookies(tag+" cookie terkirim", r.sentCookies, &out)
			hrShowCookies(tag+" cookie disimpan", r.setCookies, &out)
		}
		side("A", a.Diff.A, resA, errA)
		side("B", a.Diff.B, resB, errB)
		if errA != "" || errB != "" {
			return out.String()
		}

		// status & header
		changes := 0
		if resA.code != resB.code {
			fmt.Fprintf(&out, "PERBEDAAN Status : A=%d vs B=%d\n", resA.code, resB.code)
			changes++
		}
		keys := map[string]bool{}
		for _, k := range hrHeaderKeys(resA.header) {
			keys[k] = true
		}
		for _, k := range hrHeaderKeys(resB.header) {
			keys[k] = true
		}
		var hdrDiffs []string
		for k := range keys {
			va, vb := resA.header.Get(k), resB.header.Get(k)
			if va != vb {
				hdrDiffs = append(hdrDiffs, fmt.Sprintf("  %s: A=%q vs B=%q", k, truncRunes(va, 60), truncRunes(vb, 60)))
			}
		}
		sort.Strings(hdrDiffs)
		if len(hdrDiffs) > 0 {
			out.WriteString("PERBEDAAN Header:\n" + strings.Join(hdrDiffs, "\n") + "\n")
			changes++
		}

		// body: baris unik di masing-masing sisi
		bodyA := strings.Split(string(resA.body), "\n")
		bodyB := strings.Split(string(resB.body), "\n")
		setB := map[string]bool{}
		for _, l := range bodyB {
			setB[strings.TrimSpace(l)] = true
		}
		setA := map[string]bool{}
		for _, l := range bodyA {
			setA[strings.TrimSpace(l)] = true
		}
		var onlyA, onlyB []string
		for _, l := range bodyA {
			t := strings.TrimSpace(l)
			if t != "" && !setB[t] {
				onlyA = append(onlyA, t)
			}
		}
		for _, l := range bodyB {
			t := strings.TrimSpace(l)
			if t != "" && !setA[t] {
				onlyB = append(onlyB, t)
			}
		}
		// hilangkan duplikat berurutan agar ringkas
		onlyA = dedupLines(onlyA)
		onlyB = dedupLines(onlyB)
		if len(onlyA) > 0 || len(onlyB) > 0 {
			changes++
			fmt.Fprintf(&out, "PERBEDAAN Body: %d baris hanya di A, %d baris hanya di B\n", len(onlyA), len(onlyB))
			for _, l := range capLines(onlyA, 8) {
				fmt.Fprintf(&out, "  A+%s\n", truncRunes(l, 160))
			}
			for _, l := range capLines(onlyB, 8) {
				fmt.Fprintf(&out, "  B+%s\n", truncRunes(l, 160))
			}
		}
		if changes == 0 {
			out.WriteString("KESIMPULAN : respons IDENTIK (status, header, body) — tidak ada indikasi perbedaan akses/konten.\n")
		} else {
			out.WriteString("KESIMPULAN : ada perbedaan nyata — jika A dan B adalah dua identitas pada endpoint yang sama, ini indikasi kuat broken access control/IDOR; verifikasi isi body di atas.\n")
		}
		// bawa body ringkas A & B untuk konteks
		if len(onlyA) == 0 && len(onlyB) == 0 && resA.bodyLen != resB.bodyLen {
			fmt.Fprintf(&out, "Catatan: panjang body berbeda (%d vs %d) tanpa baris unik — kemungkinan data dinamis/timestamp.\n", resA.bodyLen, resB.bodyLen)
		}
		return out.String()
	}

	// ---- mode request tunggal ----
	var out strings.Builder
	fmt.Fprintf(&out, "=== HTTP REQUEST ===\nRequest  : %s %s\n", strings.ToUpper(orDefault(a.Method, "GET")), a.URL)
	if session != "" {
		fmt.Fprintf(&out, "Sesi     : %q\n", session)
	} else {
		out.WriteString("Sesi     : stateless (cookie tidak dikirim/disimpan)\n")
	}
	if len(a.Headers) > 0 {
		keys := make([]string, 0, len(a.Headers))
		for k := range a.Headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out.WriteString("Header   : ")
		var parts []string
		for _, k := range keys {
			parts = append(parts, k+": "+truncRunes(a.Headers[k], 80))
		}
		out.WriteString(strings.Join(parts, "; ") + "\n")
	}
	if a.Body != "" {
		fmt.Fprintf(&out, "Body     : %s\n", truncRunes(a.Body, 300))
	}
	r, errS := hrDo(hrSide{URL: a.URL, Method: a.Method, Headers: a.Headers, Body: a.Body, Session: session}, timeout, maxBody, a.VerifyTLS, a.FollowRedirects, save)
	if errS != "" {
		return out.String() + "GAGAL    : " + errS + "\n"
	}
	fmt.Fprintf(&out, "Status   : %s | %d ms | %d byte", r.status, r.elapsedMs, r.bodyLen)
	if r.tlsUsed {
		out.WriteString(" | TLS")
	}
	out.WriteString("\n")
	hrShowCookies("Cookie terkirim (dari jar)", r.sentCookies, &out)
	hrShowCookies("Cookie disimpan (Set-Cookie)", r.setCookies, &out)
	if !a.FollowRedirects && r.code >= 300 && r.code < 400 {
		fmt.Fprintf(&out, "Redirect : %s -> %q (tidak diikuti)\n", r.status, r.header.Get("Location"))
	}
	out.WriteString("Header Respons:\n")
	for _, k := range hrHeaderKeys(r.header) {
		for _, v := range r.header[k] {
			fmt.Fprintf(&out, "  %s: %s\n", k, truncRunes(v, 200))
		}
	}
	hrBodySection(r, &out)
	return out.String()
}

// ---------- util kecil ----------

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func truncRunes(s string, n int) string {
	if rn := []rune(s); len(rn) > n {
		return string(rn[:n]) + "…"
	}
	return s
}

func dedupLines(in []string) []string {
	var out []string
	var prev string
	for i, l := range in {
		if i > 0 && l == prev {
			continue
		}
		prev = l
		out = append(out, l)
	}
	return out
}

func capLines(in []string, n int) []string {
	if len(in) <= n {
		return in
	}
	return append(append([]string{}, in[:n]...), fmt.Sprintf("…(%d baris lagi)", len(in)-n))
}
