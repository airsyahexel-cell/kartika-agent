//go:build !linux && !darwin

// tui_other.go — stub mode layar penuh untuk platform tanpa termios
// (mis. Windows): semua operasi no-op; posko jatuh ke mode CLI biasa.
package ui

import (
	"bufio"
	"fmt"
	"os"
)

// EnterAlt tidak melakukan apa-apa pada platform ini.
func EnterAlt() {}

// ExitAlt tidak melakukan apa-apa pada platform ini.
func ExitAlt() {}

// TUIAvailable selalu false — mode layar penuh tidak tersedia.
func TUIAvailable() bool { return false }

// OnResize tidak melakukan apa-apa pada platform ini.
func OnResize(func()) {}

// TUI adalah stub layar penuh (tidak aktif).
type TUI struct{}

// NewTUI mengembalikan stub.
func NewTUI() *TUI { return &TUI{} }

// Append tidak melakukan apa-apa (stub).
func (t *TUI) Append(string) {}

// Clear tidak melakukan apa-apa (stub).
func (t *TUI) Clear() {}

// SetStatus tidak melakukan apa-apa (stub).
func (t *TUI) SetStatus(string) {}

// SetLive tidak melakukan apa-apa (stub).
func (t *TUI) SetLive(string, bool) {}

// SetLiveInput tidak melakukan apa-apa (stub) - pratinjau ketikan langsung
// hanya tersedia pada mode layar penuh unix (linux/darwin).
func (t *TUI) SetLiveInput(string) {}

// CommitLive tidak melakukan apa-apa (stub).
func (t *TUI) CommitLive() {}

// Resize tidak melakukan apa-apa (stub).
func (t *TUI) Resize() {}

// SetThinking tidak melakukan apa-apa (stub).
func (t *TUI) SetThinking(bool, string) {}

// Scroll tidak melakukan apa-apa (stub).
func (t *TUI) Scroll(int) {}

// ScrollTo tidak melakukan apa-apa (stub).
func (t *TUI) ScrollTo(int) {}

// AddTab tidak melakukan apa-apa (stub).
func (t *TUI) AddTab() {}

// SwitchTab tidak melakukan apa-apa (stub).
func (t *TUI) SwitchTab(int) {}

// SetTabHook tidak melakukan apa-apa (stub).
func (t *TUI) SetTabHook(func(int)) {}

// SetTabCloseHook tidak melakukan apa-apa (stub) — klik ikon × pada header
// hanya ada di mode layar penuh unix.
func (t *TUI) SetTabCloseHook(func(int)) {}

// SetHover tidak melakukan apa-apa (stub) — sorotan mouse hanya di unix.
func (t *TUI) SetHover(tab int, closeBtn, addBtn bool) {}

// SetTabTitle tidak melakukan apa-apa (stub).
func (t *TUI) SetTabTitle(string) {}

// Current selalu 0 (stub).
func (t *TUI) Current() int { return 0 }

// ReadInputTUI membaca satu baris polos pada platform ini.
func ReadInputTUI(t *TUI, prefix string) (string, error) {
	fmt.Print(Prompt(prefix + " "))
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
}

// ReadPickerTUI tidak tersedia pada platform ini — pemanggil (main.go)
// otomatis memakai fallback daftar bernomor di CLI polos.
func ReadPickerTUI(title string, items []PickerItem) (int, bool) { return -1, false }
