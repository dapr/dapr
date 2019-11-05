package memcall

import (
	"runtime"
)

// MemoryProtectionFlag specifies some particular memory protection flag.
type MemoryProtectionFlag struct {
	// NOACCESS  := 1 (0001)
	// READ      := 2 (0010)
	// WRITE     := 4 (0100) // unused
	// READWRITE := 6 (0110)

	flag byte
}

// NoAccess specifies that the memory should be marked unreadable and immutable.
func NoAccess() MemoryProtectionFlag {
	return MemoryProtectionFlag{1}
}

// ReadOnly specifies that the memory should be marked read-only (immutable).
func ReadOnly() MemoryProtectionFlag {
	return MemoryProtectionFlag{2}
}

// ReadWrite specifies that the memory should be made readable and writable.
func ReadWrite() MemoryProtectionFlag {
	return MemoryProtectionFlag{6}
}

// ErrInvalidFlag indicates that a given memory protection flag is undefined.
const ErrInvalidFlag = "<memcall> memory protection flag is undefined"

// Wipes a given byte slice.
func wipe(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
	runtime.KeepAlive(buf)
}
