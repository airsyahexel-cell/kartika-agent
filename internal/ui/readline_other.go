//go:build !linux && !darwin

// readline_other.go — fallback editor baris tanpa mode raw (platform lain,
// mis. Windows): tanpa menu saran, input baris biasa.
package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// SetInterruptHandler tidak dipakai pada platform ini (tanpa mode raw).
func SetInterruptHandler(func()) {}

// SetCtrlBHandler tidak dipakai pada platform ini (tanpa mode raw).
func SetCtrlBHandler(func()) {}

// KeyTap tidak aktif pada platform ini (tanpa mode raw).
type KeyTap struct{}

// NewKeyTap mengembalikan penangkap tombol pasif (no-op) pada platform ini.
func NewKeyTap() *KeyTap { return &KeyTap{} }

// Stop tidak melakukan apa-apa.
func (k *KeyTap) Stop() {}

var pipeStdinReader = bufio.NewReader(os.Stdin)

// ReadInput membaca satu baris tanpa menu saran (fallback).
func ReadInput(prefix string) (string, error) {
	fmt.Print(Prompt(prefix + " "))
	line, err := pipeStdinReader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
