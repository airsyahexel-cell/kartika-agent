package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRunCmdBasic(t *testing.T) {
	reg := NewRegistry()
	RegisterBuiltins(reg, ".")
	if !reg.Has("run_cmd") {
		t.Fatal("run_cmd tidak terdaftar")
	}
	out := reg.Call("run_cmd", json.RawMessage(`{"command":"echo pwned","timeout_sec":10}`))
	if !strings.Contains(out, "pwned") || !strings.Contains(out, "Exit 0") {
		t.Fatalf("output tidak sesuai:\n%s", out)
	}
}

func TestRunCmdTimeout(t *testing.T) {
	reg := NewRegistry()
	RegisterBuiltins(reg, ".")
	out := reg.Call("run_cmd", json.RawMessage(`{"command":"sleep 10","timeout_sec":1}`))
	if !strings.Contains(out, "TIMEOUT") {
		t.Fatalf("harus TIMEOUT:\n%s", out)
	}
}

func TestRunCmdExitCode(t *testing.T) {
	reg := NewRegistry()
	RegisterBuiltins(reg, ".")
	out := reg.Call("run_cmd", json.RawMessage(`{"command":"ls /nonexistent-dir-x","timeout_sec":10}`))
	if !strings.Contains(out, "Exit 2") && !strings.Contains(out, "Exit 1") {
		t.Fatalf("kode exit tidak terbaca:\n%s", out)
	}
}
