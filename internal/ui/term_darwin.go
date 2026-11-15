//go:build darwin

// term_darwin.go — kontrol termios untuk editor baris mode-raw (macOS).
package ui

import (
	"syscall"
	"unsafe"
)

// tcGet membaca atribut terminal saat ini (macOS).
func tcGet(fd int, t *syscall.Termios) error {
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd),
		uintptr(syscall.TIOCGETA), uintptr(unsafe.Pointer(t)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// tcSet menerapkan atribut terminal (macOS).
func tcSet(fd int, t *syscall.Termios) error {
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd),
		uintptr(syscall.TIOCSETA), uintptr(unsafe.Pointer(t)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// makeRaw memasuki mode raw: tanpa echo, tanpa buffering baris, tanpa
// sinyal (Ctrl+C dibaca sebagai byte 0x03, bukan SIGINT).
func makeRaw(fd int) (*syscall.Termios, error) {
	var old syscall.Termios
	if err := tcGet(fd, &old); err != nil {
		return nil, err
	}
	raw := old
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := tcSet(fd, &raw); err != nil {
		return nil, err
	}
	return &old, nil
}

// makeStreamRaw memasuki mode setengah-raw untuk penangkap tombol selama
// streaming: echo & canonical mati (byte terbaca seketika), ISIG JUGA mati
// (Ctrl+C terbaca sebagai byte 0x03 oleh KeyTap — dipakai untuk menghentikan
// giliran chat, bukan menghentikan program), VMIN=0/VTIME=1 (poll 100 ms).
func makeStreamRaw(fd int) (*syscall.Termios, error) {
	var old syscall.Termios
	if err := tcGet(fd, &old); err != nil {
		return nil, err
	}
	raw := old
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.ISIG
	raw.Cc[syscall.VMIN] = 0
	raw.Cc[syscall.VTIME] = 1
	if err := tcSet(fd, &raw); err != nil {
		return nil, err
	}
	return &old, nil
}
