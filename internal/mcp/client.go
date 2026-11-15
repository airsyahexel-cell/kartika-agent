// Package mcp mengimplementasikan MCP (Model Context Protocol) Client
// secara native di Golang menggunakan 100% standard library:
// JSON-RPC 2.0 over Stdio.
//
// Alur: baca mcp.json -> spawn MCP Server sebagai child process ->
// handshake `initialize` -> notifikasi `notifications/initialized` ->
// `tools/list` untuk discovery -> `tools/call` untuk eksekusi.
//
// Setiap pesan dikirim sebagai satu baris JSON (newline-delimited).
package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ProtocolVersion versi protokol MCP yang diusulkan klien.
const ProtocolVersion = "2024-11-05"

const requestTimeout = 120 * time.Second

// ServerConfig adalah konfigurasi satu MCP Server pada mcp.json.
type ServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type configFile struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

// LoadConfig membaca dan memvalidasi file konfigurasi MCP.
func LoadConfig(path string) (map[string]ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cf configFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("JSON tidak valid: %w", err)
	}
	if len(cf.MCPServers) == 0 {
		return nil, fmt.Errorf("tidak ada entri 'mcpServers'")
	}
	return cf.MCPServers, nil
}

// ToolInfo adalah deskripsi satu tool hasil tools/list.
type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Client adalah koneksi ke satu MCP Server (child process).
type Client struct {
	Name    string
	Server  string // nama dari serverInfo
	Version string
	Tools   []ToolInfo

	cmd       *exec.Cmd
	stdin     io.WriteCloser
	rawStdout io.ReadCloser
	br        *bufio.Reader
	errBuf    *errBuffer

	mu     sync.Mutex
	nextID int
}

// errBuffer menampung stderr child process secara terbatas untuk diagnostik.
type errBuffer struct {
	buf bytes.Buffer
	max int
	cut bool
}

func (b *errBuffer) Write(p []byte) (int, error) {
	if b.buf.Len() < b.max {
		room := b.max - b.buf.Len()
		if len(p) > room {
			b.buf.Write(p[:room])
			b.cut = true
		} else {
			b.buf.Write(p)
		}
	} else {
		b.cut = true
	}
	return len(p), nil
}

func (b *errBuffer) String() string {
	s := b.buf.String()
	if b.cut {
		s += " …(terpotong)"
	}
	return strings.TrimSpace(s)
}

// Start melakukan spawn child process + handshake + tools discovery.
func Start(name string, sc ServerConfig) (*Client, error) {
	if sc.Command == "" {
		return nil, fmt.Errorf("field 'command' kosong")
	}
	if _, err := exec.LookPath(sc.Command); err != nil {
		return nil, fmt.Errorf("executable '%s' tidak ditemukan di PATH", sc.Command)
	}
	cmd := exec.Command(sc.Command, sc.Args...)
	env := os.Environ()
	for k, v := range sc.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	eb := &errBuffer{max: 8192}
	cmd.Stderr = eb

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("gagal spawn proses: %w", err)
	}

	c := &Client{
		Name:      name,
		cmd:       cmd,
		stdin:     stdin,
		rawStdout: stdout,
		br:        bufio.NewReaderSize(stdout, 4*1024*1024),
		errBuf:    eb,
	}

	if err := c.handshake(); err != nil {
		c.Shutdown()
		if msg := eb.String(); msg != "" {
			return nil, fmt.Errorf("%w | stderr: %s", err, firstLine(msg))
		}
		return nil, err
	}
	if err := c.discoverTools(); err != nil {
		c.Shutdown()
		return nil, err
	}
	return c, nil
}

func (c *Client) handshake() error {
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "kartika-agent", "version": "1.0.0"},
	}
	res, err := c.request("initialize", params)
	if err != nil {
		return err
	}
	if si, ok := res["serverInfo"].(map[string]any); ok {
		c.Server, _ = si["name"].(string)
		c.Version, _ = si["version"].(string)
	}
	// Notifikasi initialized (tanpa id) — menandakan handshake selesai.
	c.mu.Lock()
	_ = c.writeMsg(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized", "params": map[string]any{}})
	c.mu.Unlock()
	return nil
}

func (c *Client) discoverTools() error {
	res, err := c.request("tools/list", map[string]any{})
	if err != nil {
		return err
	}
	raw, _ := res["tools"].([]any)
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		tname, _ := m["name"].(string)
		desc, _ := m["description"].(string)
		schema, _ := m["inputSchema"].(map[string]any)
		if tname == "" {
			continue
		}
		c.Tools = append(c.Tools, ToolInfo{Name: tname, Description: desc, InputSchema: schema})
	}
	return nil
}

// request mengirim JSON-RPC request dan menunggu respons dengan id yang cocok.
// Notifikasi dan request dari server diabaikan/dibalas error tanpa mengganggu.
func (c *Client) request(method string, params any) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	id := c.nextID
	if err := c.writeMsg(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, fmt.Errorf("gagal menulis ke stdin MCP '%s': %w", c.Name, err)
	}
	c.setDeadline(time.Now().Add(requestTimeout))
	for {
		line, err := c.readLine()
		if err != nil {
			return nil, fmt.Errorf("membaca respons '%s' dari '%s': %w", method, c.Name, err)
		}
		var msg struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Result  map[string]any  `json:"result"`
			Error   *rpcError       `json:"error"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			continue // baris noise non-JSON dibuang
		}
		if msg.Method != "" {
			// Pesan server-initiated: balas error untuk request, abaikan notifikasi.
			if len(msg.ID) > 0 && string(msg.ID) != "null" {
				_ = c.writeMsg(map[string]any{
					"jsonrpc": "2.0", "id": msg.ID,
					"error": map[string]any{"code": -32601, "message": "kartika-agent tidak mendukung method server-initiated '" + msg.Method + "'"},
				})
			}
			continue
		}
		if !idMatches(msg.ID, id) {
			continue // respons usang dari request sebelumnya
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("RPC error %d: %s", msg.Error.Code, msg.Error.Message)
		}
		if msg.Result == nil {
			msg.Result = map[string]any{}
		}
		return msg.Result, nil
	}
}

func idMatches(raw json.RawMessage, id int) bool {
	if len(raw) == 0 {
		return false
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return n == id
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s == fmt.Sprint(id)
	}
	return false
}

func (c *Client) setDeadline(t time.Time) {
	type deadliner interface{ SetReadDeadline(time.Time) error }
	if d, ok := c.rawStdout.(deadliner); ok {
		_ = d.SetReadDeadline(t)
	}
}

// readLine membaca satu baris JSON; baris kosong/noise dilewati.
func (c *Client) readLine() ([]byte, error) {
	for {
		line, err := c.br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed[0] != '{' {
			continue
		}
		return []byte(trimmed), nil
	}
}

// writeMsg menulis satu pesan JSON (newline-delimited). Asumsi: mutex sudah dipegang.
func (c *Client) writeMsg(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.stdin.Write(data)
	return err
}

// CallTool mengeksekusi tools/call dan menyatukan konten teks hasilnya.
func (c *Client) CallTool(name string, args json.RawMessage) (string, bool, error) {
	arguments := map[string]any{}
	if len(args) > 0 {
		var m map[string]any
		if json.Unmarshal(args, &m) == nil && m != nil {
			arguments = m
		}
	}
	res, err := c.request("tools/call", map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		return "", false, err
	}
	isErr, _ := res["isError"].(bool)
	content, _ := res["content"].([]any)
	var sb strings.Builder
	for _, item := range content {
		cm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch cm["type"] {
		case "text":
			t, _ := cm["text"].(string)
			sb.WriteString(t)
			sb.WriteString("\n")
		default:
			fmt.Fprintf(&sb, "[konten non-teks diabaikan: %v]\n", cm["type"])
		}
	}
	out := strings.TrimRight(sb.String(), "\n")
	if out == "" {
		out = "(modul MCP tidak mengembalikan konten)"
	}
	return out, isErr, nil
}

// Shutdown menutup sesi: stdin ditutup, proses diberi waktu keluar, lalu dipaksa mati.
func (c *Client) Shutdown() {
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd == nil || c.cmd.Process == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_ = c.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = c.cmd.Process.Kill()
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
