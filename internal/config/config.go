// Package config memuat konfigurasi engine AI KARTIKA-AGENT.
// Sumber: variabel lingkungan (KARTIKA_*) dan file .env (opsional, otomatis dimuat).
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ProviderURL menyimpan base URL OpenAI-Compatible untuk tiap profil provider.
type ProviderURL struct {
	Name string
	URL  string
}

// ProviderPresets adalah daftar profil provider yang dikenali flag --provider.
var ProviderPresets = []ProviderURL{
	{"ollama", "http://localhost:11434/v1"},
	{"vllm", "http://localhost:8000/v1"},
	{"groq", "https://api.groq.com/openai/v1"},
	{"openai", "https://api.openai.com/v1"},
	{"openrouter", "https://openrouter.ai/api/v1"},
	{"gemini", "https://generativelanguage.googleapis.com/v1beta/openai"},
	{"nvidia", "https://integrate.api.nvidia.com/v1"},
}

// Config adalah konfigurasi runtime agen.
type Config struct {
	Provider    string // profil provider (boleh kosong)
	APIURL      string // base URL OpenAI-Compatible (tanpa /chat/completions)
	APIKey      string // kunci API (bearer token)
	Model       string // nama model
	MaxSteps    int    // batas langkah penalaran ReAct per misi
	Temperature float64
	TimeoutSec  int    // timeout HTTP klien LLM (detik)
	MCPConfig   string // path file konfigurasi MCP
	WriteScope  string // cakupan direktori untuk write_file
}

// Load membaca .env (jika ada), lalu membangun konfigurasi dari env dengan default yang aman.
//
// Urutan prioritas konfigurasi (yang pertama ditemukan menang):
//  1. Variabel lingkungan asli (KARTIKA_*) yang sudah di-set sebelum proses jalan
//  2. File .env di direktori kerja saat ini (override per proyek/misi)
//  3. File ~/.config/kartika/.env (konfigurasi global — agar binary yang
//     dipasang di PATH bisa dipanggil dari direktori mana pun)
//  4. Nilai default bawaan program
func Load(provider string) *Config {
	loadDotEnv(".env")
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		loadDotEnv(filepath.Join(home, ".config", "kartika", ".env"))
	}

	c := &Config{Provider: strings.ToLower(strings.TrimSpace(provider))}

	c.APIURL = getEnv("KARTIKA_API_URL", "")
	if c.APIURL == "" {
		for _, p := range ProviderPresets {
			if c.Provider == p.Name {
				c.APIURL = p.URL
				break
			}
		}
	}
	if c.APIURL == "" {
		c.APIURL = "http://localhost:11434/v1" // default: Ollama lokal (mode air-gapped)
	}

	c.APIKey = getEnv("KARTIKA_API_KEY", "ollama")
	c.Model = getEnv("KARTIKA_MODEL", "llama3.3")
	c.MaxSteps = getEnvInt("KARTIKA_MAX_STEPS", 160)
	c.Temperature = getEnvFloat("KARTIKA_TEMPERATURE", 0.2)
	c.TimeoutSec = getEnvInt("KARTIKA_TIMEOUT", 180)
	c.MCPConfig = getEnv("KARTIKA_MCP_CONFIG", "mcp.json")
	c.WriteScope = getEnv("KARTIKA_WRITE_SCOPE", ".")
	return c
}

// IsLocal melaporkan apakah engine berjalan di mesin lokal (ollama/vLLM/localhost).
func (c *Config) IsLocal() bool {
	p := strings.ToLower(c.Provider)
	if p == "ollama" || p == "vllm" {
		return true
	}
	u := strings.ToLower(c.APIURL)
	return strings.Contains(u, "localhost") || strings.Contains(u, "127.0.0.1") || strings.Contains(u, "0.0.0.0") || strings.Contains(u, "::1") || strings.Contains(u, ":11434") || strings.Contains(u, ":8000")
}

// SaveEnvVar menyimpan variabel ke file ~/.config/kartika/.env (dan .env lokal jika ada).
func SaveEnvVar(key, val string) error {
	_ = os.Setenv(key, val)

	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		globalDir := filepath.Join(home, ".config", "kartika")
		_ = os.MkdirAll(globalDir, 0755)
		globalPath := filepath.Join(globalDir, ".env")
		_ = updateEnvFile(globalPath, key, val)
	}

	if _, err := os.Stat(".env"); err == nil {
		_ = updateEnvFile(".env", key, val)
	}
	return nil
}

func updateEnvFile(path, key, val string) error {
	var lines []string
	found := false
	if data, err := os.ReadFile(path); err == nil {
		sc := bufio.NewScanner(strings.NewReader(string(data)))
		for sc.Scan() {
			line := sc.Text()
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "#") && strings.HasPrefix(trimmed, key+"=") {
				lines = append(lines, key+"="+val)
				found = true
			} else {
				lines = append(lines, line)
			}
		}
	}
	if !found {
		lines = append(lines, key+"="+val)
	}
	content := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(path, []byte(content), 0600)
}

// loadDotEnv memuat pasangan KEY=VALUE dari file .env hanya untuk variabel
// yang belum ada di lingkungan (env asli selalu menang).
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		val = strings.Trim(val, `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func getEnvFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}
