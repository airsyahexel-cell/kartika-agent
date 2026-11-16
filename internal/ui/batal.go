// batal.go — sentinel lintas platform untuk pembatalan sub-alur.
package ui

import "errors"

// ErrBatal dikembalikan ReadInputTUIBatal ketika operator menekan Ctrl+C di
// tengah sub-alur (wizard konfigurasi, pasang skill, tambah server MCP).
// Pemanggil memperlakukannya sebagai "langkah ini dibatalkan" — bukan alasan
// menghentikan program dan menutup sesi chat.
var ErrBatal = errors.New("dibatalkan operator")
