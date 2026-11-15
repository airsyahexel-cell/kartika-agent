//go:build linux || darwin
package ui

import (
	"bufio"
	"strings"
	"testing"
)

// TestReadKeyCtrlB memastikan byte 0x02 (Ctrl+B) dikenali sebagai kCtrlB,
// bukan rune biasa — dasar toggle pemikiran model.
func TestReadKeyCtrlB(t *testing.T) {
	ev := readKey(bufio.NewReader(strings.NewReader("\x02")))
	if ev.kind != kCtrlB {
		t.Fatalf("byte 0x02 harus kCtrlB, dapat kind=%d", ev.kind)
	}
	ev = readKey(bufio.NewReader(strings.NewReader("a")))
	if ev.kind != kRune || ev.r != 'a' {
		t.Fatalf("huruf biasa rusak: kind=%d r=%q", ev.kind, ev.r)
	}
}
