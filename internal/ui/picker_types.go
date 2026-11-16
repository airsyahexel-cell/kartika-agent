// picker_types.go — tipe data picker yang dipakai lintas platform.
//
// PickerItem sengaja dipisah dari picker.go (yang hanya dibangun pada
// linux/darwin karena memakai termios): main.go menyusun daftar PickerItem
// di semua platform, sementara tampilan interaktifnya (ReadPickerTUI)
// tersedia di unix dan menjadi fallback daftar bernomor pada platform lain.
package ui

// PickerItem adalah satu pilihan dalam picker.
type PickerItem struct {
	Label string // teks utama
	Desc  string // keterangan tambahan (tampil redup)
}
