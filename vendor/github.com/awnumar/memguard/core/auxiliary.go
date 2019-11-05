package core

import (
	"os"
	"unsafe"
)

var (
	// Ascertain and store the system memory page size.
	pageSize = os.Getpagesize()
)

// Round a length to a multiple of the system page size.
func roundToPageSize(length int) int {
	return (length + (pageSize - 1)) & (^(pageSize - 1))
}

// Convert a pointer and length to a byte slice that describes that memory.
func getBytes(ptr *byte, len int) []byte {
	var sl = struct {
		addr uintptr
		len  int
		cap  int
	}{uintptr(unsafe.Pointer(ptr)), len, len}
	return *(*[]byte)(unsafe.Pointer(&sl))
}
