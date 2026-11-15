package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigIsLocal(t *testing.T) {
	cLocalOllama := &Config{Provider: "ollama", APIURL: "http://localhost:11434/v1"}
	if !cLocalOllama.IsLocal() {
		t.Errorf("expected ollama to be local")
	}

	cLocalVLLM := &Config{Provider: "vllm", APIURL: "http://127.0.0.1:8000/v1"}
	if !cLocalVLLM.IsLocal() {
		t.Errorf("expected vllm to be local")
	}

	cDefault := &Config{APIURL: "http://localhost:11434/v1"}
	if !cDefault.IsLocal() {
		t.Errorf("expected default localhost to be local")
	}

	cCloudGroq := &Config{Provider: "groq", APIURL: "https://api.groq.com/openai/v1"}
	if cCloudGroq.IsLocal() {
		t.Errorf("expected groq to NOT be local")
	}

	cCloudOpenAI := &Config{Provider: "openai", APIURL: "https://api.openai.com/v1"}
	if cCloudOpenAI.IsLocal() {
		t.Errorf("expected openai to NOT be local")
	}
}

func TestSaveEnvVar(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	err := os.WriteFile(envPath, []byte("KARTIKA_MODEL=old-model\nFOO=bar\n"), 0600)
	if err != nil {
		t.Fatal(err)
	}

	err = updateEnvFile(envPath, "KARTIKA_MODEL", "new-model")
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "KARTIKA_MODEL=new-model") {
		t.Errorf("expected content to contain KARTIKA_MODEL=new-model, got: %s", content)
	}
	if strings.Contains(content, "KARTIKA_MODEL=old-model") {
		t.Errorf("expected content to not contain KARTIKA_MODEL=old-model")
	}
}
