// Package llm mengimplementasikan klien Chat Completions dengan skema
// OpenAI-Compatible (function calling / tools) menggunakan 100% standard
// library Golang — tanpa dependensi eksternal.
//
// Satu klien yang sama dapat dipakai untuk: Ollama, vLLM (air-gapped/lokal),
// Groq, OpenAI, OpenRouter, dan endpoint Gemini OpenAI-Compatible.
// Cukup ganti KARTIKA_API_URL / KARTIKA_API_KEY / KARTIKA_MODEL.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Message adalah satu pesan dalam percakapan (system/user/assistant/tool).
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall adalah permintaan pemanggilan modul dari engine.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ToolDef adalah definisi modul dalam JSON Schema (format OpenAI).
type ToolDef struct {
	Type     string       `json:"type"`
	Function ToolFuncSpec `json:"function"`
}

// ToolFuncSpec memuat nama, deskripsi, dan parameter schema modul.
type ToolFuncSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Client adalah klien HTTP ke endpoint OpenAI-Compatible.
type Client struct {
	URL    string
	APIKey string
	Model  string
	Temp   float64
	HTTP   *http.Client

	cancelMu sync.Mutex
	cancel   chan struct{} // kanal batal request yang sedang berjalan
}

// Interrupt membatalkan request yang sedang berjalan (bila ada): koneksi
// ditutup segera sehingga Chat/ChatStream kembali dengan error. Dipanggil
// saat operator menekan Ctrl+C untuk menghentikan giliran chat.
func (c *Client) Interrupt() {
	c.cancelMu.Lock()
	if c.cancel != nil {
		select {
		case <-c.cancel: // sudah tertutup
		default:
			close(c.cancel)
		}
	}
	c.cancelMu.Unlock()
}

// ctxWithCancel membuat context yang dibatalkan saat Interrupt() dipanggil,
// dan mendaftarkan dirinya sebagai "request berjalan" agar Interrupt tahu
// channel mana yang harus ditutup. Pembersihan dilakukan via fungsi balikan.
func (c *Client) ctxWithCancel() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan struct{})
	c.cancelMu.Lock()
	c.cancel = ch
	c.cancelMu.Unlock()
	go func() {
		select {
		case <-ch:
			cancel()
		case <-ctx.Done():
		}
	}()
	cleanup := func() {
		c.cancelMu.Lock()
		if c.cancel == ch {
			c.cancel = nil
		}
		c.cancelMu.Unlock()
		close(ch)
		cancel()
	}
	return ctx, cleanup
}

// isCanceled melaporkan apakah error terakhir berasal dari pembatalan
// (Interrupt) — dipakai untuk menghentikan siklus retry segera.
func isCanceled(err error) bool {
	return err != nil && strings.Contains(err.Error(), "context canceled")
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []ToolDef `json:"tools,omitempty"`
	ToolChoice  any       `json:"tool_choice,omitempty"`
	Temperature float64   `json:"temperature"`
	Stream      bool      `json:"stream,omitempty"`
}

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Error *apiError `json:"error,omitempty"`
}

// streamDelta adalah satu potongan jawaban dalam mode streaming (SSE).
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			Reasoning string `json:"reasoning_content"` // alur berpikir (NVIDIA NIM, DeepSeek, dsb.)
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *apiError `json:"error,omitempty"`
}

// NewClient membangun klien LLM baru dengan timeout tertentu.
func NewClient(url, apiKey, model string, temp float64, timeout time.Duration) *Client {
	return &Client{
		URL:    strings.TrimRight(url, "/"),
		APIKey: apiKey,
		Model:  model,
		Temp:   temp,
		HTTP:   &http.Client{Timeout: timeout},
	}
}

// Chat mengirim percakapan + daftar modul, mengembalikan pesan balasan
// (bisa berisi tool_calls atau konten final). Mencoba ulang hingga 3 kali
// untuk kegagalan jaringan/5xx/429.
func (c *Client) Chat(msgs []Message, tools []ToolDef) (Message, error) {
	req := chatRequest{
		Model:       c.Model,
		Messages:    msgs,
		Tools:       tools,
		ToolChoice:  "auto",
		Temperature: c.Temp,
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}
		body, err := json.Marshal(req)
		if err != nil {
			return Message{}, err
		}
		ctx, cleanup := c.ctxWithCancel()
		httpReq, err := http.NewRequestWithContext(ctx, "POST", c.URL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			cleanup()
			return Message{}, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if c.APIKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
		}
		resp, err := c.HTTP.Do(httpReq)
		if err != nil {
			cleanup()
			if isCanceled(err) {
				return Message{}, fmt.Errorf("chat dihentikan operator (ctrl+c)")
			}
			lastErr = fmt.Errorf("koneksi ke engine gagal (%s): %w", c.URL, err)
			continue
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		cleanup()
		if resp.StatusCode >= 400 {
			lastErr = fmt.Errorf("engine menolak (HTTP %d): %s", resp.StatusCode, snippet(data, 300))
			if resp.StatusCode == 429 || resp.StatusCode >= 500 {
				continue // dapat dicoba ulang
			}
			return Message{}, lastErr
		}
		var cr chatResponse
		if err := json.Unmarshal(data, &cr); err != nil {
			return Message{}, fmt.Errorf("respons engine tidak valid (bukan JSON): %w", err)
		}
		if cr.Error != nil {
			return Message{}, fmt.Errorf("engine error: %s", cr.Error.Message)
		}
		if len(cr.Choices) == 0 {
			return Message{}, fmt.Errorf("engine tidak mengembalikan pilihan jawaban")
		}
		m := cr.Choices[0].Message
		if strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
			return Message{}, fmt.Errorf("engine mengembalikan respons kosong")
		}
		return m, nil
	}
	return Message{}, lastErr
}

// ChatStream sama dengan Chat, namun meminta endpoint mengalirkan jawaban
// (Server-Sent Events, "stream": true). Setiap potongan teks diteruskan ke
// onDelta segera saat diterima sehingga jawaban muncul token demi token di
// terminal — seperti chat Claude Code / opencode. Alur berpikir internal
// engine (delta "reasoning_content", mis. NVIDIA NIM) diteruskan terpisah
// ke onReasoning dan TIDAK dicampur ke jawaban utama.
//
// Ketahanan:
//   - Tool calls yang di-stream (delta "tool_calls" berbasis index) diakumulasi
//     menjadi ToolCall utuh sebelum dikembalikan.
//   - Bila endpoint tidak mendukung streaming atau koneksi streaming gagal
//     sebelum ada potongan tercetak, otomatis fallback ke Chat() biasa.
//   - Mencoba ulang hingga 3 kali untuk kegagalan jaringan/429/5xx.
func (c *Client) ChatStream(msgs []Message, tools []ToolDef, onDelta func(string), onReasoning func(string)) (Message, error) {
	req := chatRequest{
		Model:       c.Model,
		Messages:    msgs,
		Tools:       tools,
		ToolChoice:  "auto",
		Temperature: c.Temp,
		Stream:      true,
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}
		msg, emitted, err := c.streamOnce(req, onDelta, onReasoning)
		if err == nil {
			return msg, nil
		}
		if isCanceled(err) || strings.Contains(err.Error(), "ctrl+c") {
			return Message{}, err // dibatalkan operator — JANGAN retry/fallback
		}
		if emitted {
			// Sebagian teks sudah tercetak — jangan ulangi (risiko dobel).
			return Message{}, err
		}
		lastErr = err
	}
	// Fallback: endpoint mungkin tidak mendukung streaming — pakai mode biasa.
	msg, err := c.Chat(msgs, tools)
	if err != nil {
		return Message{}, lastErr
	}
	if msg.Content != "" && onDelta != nil {
		onDelta(msg.Content)
	}
	return msg, nil
}

// streamOnce menjalankan satu percobaan request streaming dan memparsing
// aliran SSE (baris "data: {...}" diakhiri "data: [DONE]").
func (c *Client) streamOnce(req chatRequest, onDelta func(string), onReasoning func(string)) (msg Message, emitted bool, err error) {
	body, err := json.Marshal(req)
	if err != nil {
		return Message{}, false, err
	}
	ctx, cleanup := c.ctxWithCancel()
	defer cleanup()
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.URL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Message{}, false, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		if isCanceled(err) {
			return Message{}, false, fmt.Errorf("chat dihentikan operator (ctrl+c)")
		}
		return Message{}, false, fmt.Errorf("koneksi ke engine gagal (%s): %w", c.URL, err)
	}
	defer resp.Body.Close()

	// Endpoint menolak streaming — biarkan fallback non-streaming menangani.
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		return Message{}, false, fmt.Errorf("engine menolak (HTTP %d): %s", resp.StatusCode, snippet(data, 300))
	}
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "application/json") {
		// Server menjawab JSON utuh (tidak mendukung stream) — parse langsung.
		data, _ := io.ReadAll(resp.Body)
		m, perr := parseChatJSON(data)
		if perr != nil {
			return Message{}, false, perr
		}
		if m.Content != "" && onDelta != nil {
			onDelta(m.Content)
		}
		return m, true, nil
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var content strings.Builder
	deltas := 0        // jumlah potongan teks yang sudah diteruskan ke onDelta
	reasonShown := false // true bila reasoning sudah mengalir ke pemanggil
	type tcAcc struct {
		call ToolCall
		args strings.Builder
	}
	acc := map[int]*tcAcc{}
	order := []int{}

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return Message{}, deltas > 0, fmt.Errorf("potongan streaming tidak valid: %w", err)
		}
		if chunk.Error != nil {
			return Message{}, deltas > 0, fmt.Errorf("engine error: %s", chunk.Error.Message)
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Reasoning != "" && onReasoning != nil {
				// Reasoning yang sudah tercetak TIDAK boleh diregenerasi —
				// kalau streaming putus di sini, usahakan lanjut tanpa retry
				// (retry meregenerasi reasoning yang sama → teks berulang).
				onReasoning(ch.Delta.Reasoning)
				reasonShown = true
			}
			if ch.Delta.Content != "" {
				content.WriteString(ch.Delta.Content)
				if onDelta != nil {
					onDelta(ch.Delta.Content)
				}
				deltas++
			}
			for _, dtc := range ch.Delta.ToolCalls {
				a := acc[dtc.Index]
				if a == nil {
					a = &tcAcc{}
					a.call.ID = dtc.ID
					a.call.Type = dtc.Type
					if a.call.Type == "" {
						a.call.Type = "function"
					}
					a.call.Function.Name = dtc.Function.Name
					acc[dtc.Index] = a
					order = append(order, dtc.Index)
				} else {
					if dtc.ID != "" {
						a.call.ID = dtc.ID
					}
					if dtc.Function.Name != "" {
						a.call.Function.Name = dtc.Function.Name
					}
				}
				a.args.WriteString(dtc.Function.Arguments)
			}
		}
	}
	if err := sc.Err(); err != nil {
		if isCanceled(err) || ctx.Err() != nil {
			return Message{}, deltas > 0 || reasonShown, fmt.Errorf("chat dihentikan operator (ctrl+c)")
		}
		return Message{}, deltas > 0 || reasonShown, fmt.Errorf("aliran streaming terputus: %w", err)
	}

	out := Message{Role: "assistant", Content: content.String()}
	for _, idx := range order {
		a := acc[idx]
		a.call.Function.Arguments = a.args.String()
		out.ToolCalls = append(out.ToolCalls, a.call)
	}
	if strings.TrimSpace(out.Content) == "" && len(out.ToolCalls) == 0 {
		return Message{}, deltas > 0, fmt.Errorf("engine mengembalikan respons streaming kosong")
	}
	return out, deltas > 0, nil
}

// parseChatJSON memparsing respons chat/completions JSON utuh (non-stream).
func parseChatJSON(data []byte) (Message, error) {
	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return Message{}, fmt.Errorf("respons engine tidak valid (bukan JSON): %w", err)
	}
	if cr.Error != nil {
		return Message{}, fmt.Errorf("engine error: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return Message{}, fmt.Errorf("engine tidak mengembalikan pilihan jawaban")
	}
	return cr.Choices[0].Message, nil
}

// snippet memotong data menjadi ringkasan satu-baris maksimal n byte.
func snippet(data []byte, n int) string {
	s := strings.TrimSpace(string(data))
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	if s == "" {
		return "(tanpa body)"
	}
	return s
}
