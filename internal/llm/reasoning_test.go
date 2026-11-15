package llm

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestStreamReasoning memastikan delta reasoning_content dialirkan ke
// onReasoning terpisah dari konten utama (toggle Ctrl+B).
func TestStreamReasoning(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"berpikir: \"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"jawaban\"}}]}\n\n" +
		"data: [DONE]\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "k", "m", 0.5, 10*time.Second)
	var reason, content strings.Builder
	_, err := c.ChatStream([]Message{{Role: "user", Content: "uji"}},
		nil,
		func(d string) { content.WriteString(d) },
		func(r string) { reason.WriteString(r) })
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if reason.String() != "berpikir: " {
		t.Fatalf("reasoning tidak diteruskan: %q", reason.String())
	}
	if content.String() != "jawaban" {
		t.Fatalf("konten utama rusak: %q", content.String())
	}
}
