// Package tools menyediakan registri modul taktis. Registri menyatukan
// modul built-in (scan_dir, read_file, http_probe, write_file) dan modul
// eksternal dari MCP Server ke dalam satu antarmuka pemanggilan seragam
// yang diekspos ke engine sebagai function calling (JSON Schema).
package tools

import (
	"encoding/json"
	"fmt"
	"sort"

	"kartika-agent/internal/llm"
)

// Tool adalah satu modul taktis.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any // JSON Schema parameter (type: object)
	Fn          func(args json.RawMessage) string
}

// Registry menyimpan seluruh modul yang tersedia untuk agen.
type Registry struct {
	names []string
	m     map[string]*Tool
}

// NewRegistry membuat registri kosong.
func NewRegistry() *Registry {
	return &Registry{m: map[string]*Tool{}}
}

// Register menambahkan modul; nama duplikat diabaikan.
func (r *Registry) Register(t *Tool) {
	if t == nil || t.Name == "" {
		return
	}
	if _, exists := r.m[t.Name]; exists {
		return
	}
	r.m[t.Name] = t
	r.names = append(r.names, t.Name)
}

// Count mengembalikan jumlah modul terdaftar.
func (r *Registry) Count() int { return len(r.m) }

// Names mengembalikan daftar nama modul terurut.
func (r *Registry) Names() []string {
	out := append([]string(nil), r.names...)
	sort.Strings(out)
	return out
}

// Has memeriksa keberadaan modul.
func (r *Registry) Has(name string) bool {
	_, ok := r.m[name]
	return ok
}

// Defs mengubah registri menjadi daftar ToolDef untuk engine.
func (r *Registry) Defs() []llm.ToolDef {
	names := r.Names()
	defs := make([]llm.ToolDef, 0, len(names))
	for _, n := range names {
		t := r.m[n]
		defs = append(defs, llm.ToolDef{
			Type: "function",
			Function: llm.ToolFuncSpec{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Schema,
			},
		})
	}
	return defs
}

// Call mengeksekusi modul dengan argumen JSON mentah dan mengembalikan
// observasi teks. Panik di dalam modul dipulihkan menjadi pesan ERROR.
func (r *Registry) Call(name string, args json.RawMessage) (out string) {
	t, ok := r.m[name]
	if !ok {
		return fmt.Sprintf("ERROR: modul '%s' tidak terdaftar pada registri.", name)
	}
	defer func() {
		if rec := recover(); rec != nil {
			out = fmt.Sprintf("ERROR: modul '%s' mengalami kegagalan internal: %v", name, rec)
		}
	}()
	return t.Fn(args)
}
