package mcp_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"kartika-agent/internal/mcp"
)

func TestPlaywrightNavigate(t *testing.T) {
	if testing.Short() {
		t.Skip("skip: butuh browser Chromium + jaringan (jalankan tanpa -short)")
	}
	cl, err := mcp.Start("playwright", mcp.ServerConfig{
		Command: "npx",
		Args:    []string{"-y", "@playwright/mcp@latest", "--browser", "chromium"},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer cl.Shutdown()
	args, _ := json.Marshal(map[string]any{"url": "https://example.com"})
	out, isErr, err := cl.CallTool("browser_navigate", args)
	fmt.Printf("isErr=%v err=%v\n---\n%s\n", isErr, err, out)
}
