package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// login sekali → cookie masuk jar → panggilan berikutnya membawanya.
func TestHTTPRequestSessionPersistence(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "PHPSESSID", Value: "s3cret123", Path: "/"})
		w.WriteHeader(200)
		fmt.Fprint(w, "login ok")
	})
	var gotCookie string
	mux.HandleFunc("/panel", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("PHPSESSID"); err == nil {
			gotCookie = c.Value
		}
		fmt.Fprintf(w, "panel untuk %s", gotCookie)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// login dengan sesi "user"
	out := runHTTPRequest([]byte(fmt.Sprintf(`{"url":%q,"method":"POST","session":"u1"}`, srv.URL+"/login")))
	if !strings.Contains(out, "login ok") || !strings.Contains(out, "PHPSESSID=s3cret123") {
		t.Fatalf("login: cookie tidak tercatat:\n%s", out)
	}
	// request berikutnya memakai jar
	out = runHTTPRequest([]byte(fmt.Sprintf(`{"url":%q,"session":"u1"}`, srv.URL+"/panel")))
	if !strings.Contains(gotCookie, "s3cret123") {
		t.Fatalf("cookie tidak terkirim ke server: gotCookie=%q", gotCookie)
	}
	if !strings.Contains(out, "panel untuk s3cret123") {
		t.Fatalf("body tanpa cookie:\n%s", out)
	}
}

// diff dua sesi dengan body berbeda = indikasi IDOR terdeteksi.
func TestHTTPRequestDiffDetectsDifference(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user", func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("X-Role") {
		case "admin":
			fmt.Fprint(w, "DATA RAHASIA ADMIN\nbaris2\nbaris3")
		default:
			fmt.Fprint(w, "data publik\nbaris2")
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	args := map[string]any{
		"diff": map[string]any{
			"a": map[string]any{"url": srv.URL + "/api/user", "headers": map[string]string{"X-Role": "admin"}},
			"b": map[string]any{"url": srv.URL + "/api/user", "headers": map[string]string{"X-Role": "user"}},
		},
	}
	raw, _ := json.Marshal(args)
	out := runHTTPRequest(raw)
	if !strings.Contains(out, "PERBEDAAN Body") || !strings.Contains(out, "DATA RAHASIA ADMIN") {
		t.Fatalf("diff tidak mendeteksi perbedaan:\n%s", out)
	}
	if !strings.Contains(out, "KESIMPULAN") {
		t.Fatalf("diff tanpa kesimpulan:\n%s", out)
	}
}

// respons identik → tanpa indikasi.
func TestHTTPRequestDiffIdentical(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sama", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "identik") })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	args := map[string]any{
		"diff": map[string]any{
			"a": map[string]any{"url": srv.URL + "/sama"},
			"b": map[string]any{"url": srv.URL + "/sama"},
		},
	}
	raw, _ := json.Marshal(args)
	out := runHTTPRequest(raw)
	if !strings.Contains(out, "IDENTIK") {
		t.Fatalf("harus dilaporkan identik:\n%s", out)
	}
}

// list_sessions menampilkan isi jar.
func TestHTTPRequestListSessions(t *testing.T) {
	out := runHTTPRequest([]byte(`{"action":"list_sessions"}`))
	if !strings.Contains(out, "=== SESI COOKIE ===") {
		t.Fatalf("list_sessions rusak:\n%s", out)
	}
}
