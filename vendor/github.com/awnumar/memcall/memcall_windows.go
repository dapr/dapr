// +build windows

package memcall

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Placeholder variable for when we need a valid pointer to zero bytes.
var _zero uintptr

// Lock is a wrapper for windows.VirtualLock()
func Lock(b []byte) error {
	if err := windows.VirtualLock(_getPtr(b), uintptr(len(b))); err != nil {
		return fmt.Errorf("<memcall> could not acquire lock on %p, limit reached? [Err: %s]", &b[0], err)
	}

	return nil
}

// Unlock is a wrapper for windows.VirtualUnlock()
func Unlock(b []byte) error {
	if err := windows.VirtualUnlock(_getPtr(b), uintptr(len(b))); err != nil {
		return fmt.Errorf("<memcall> could not free lock on %p [Err: %s]", &b[0], err)
	}

	return nil
}

// Alloc allocates a byte slice of length n and returns it.
func Alloc(n int) ([]byte, error) {
	// Allocate the memory.
	ptr, err := windows.VirtualAlloc(_zero, uintptr(n), 0x1000|0x2000, 0x4)
	if err != nil {
		return nil, fmt.Errorf("<memcall> could not allocate [Err: %s]", err)
	}

	// Convert this pointer to a slice.
	b := _getBytes(ptr, n, n)

	// Wipe it just in case there is some remnant data.
	wipe(b)

	// Return the allocated memory.
	return b, nil
}

// Free deallocates the byte slice specified.
func Free(b []byte) error {
	// Make the memory region readable and writable.
	if err := Protect(b, ReadWrite()); err != nil {
		return err
	}

	// Wipe the memory region in case of remnant data.
	wipe(b)

	// Free the memory back to the kernel.
	if err := windows.VirtualFree(_getPtr(b), uintptr(0), 0x8000); err != nil {
		return fmt.Errorf("<memcall> could not deallocate %p [Err: %s]", &b[0], err)
	}

	return nil
}

// Protect modifies the memory protection flags for a specified byte slice.
func Protect(b []byte, mpf MemoryProtectionFlag) error {
	var prot int
	if mpf.flag == ReadWrite().flag {
		prot = 0x4 // PAGE_READWRITE
	} else if mpf.flag == ReadOnly().flag {
		prot = 0x2 // PAGE_READ
	} else if mpf.flag == NoAccess().flag {
		prot = 0x1 // PAGE_NOACCESS
	} else {
		return errors.New(ErrInvalidFlag)
	}

	var oldProtect uint32
	if err := windows.VirtualProtect(_getPtr(b), uintptr(len(b)), uint32(prot), &oldProtect); err != nil {
		return fmt.Errorf("<memcall> could not set %d on %p [Err: %s]", prot, &b[0], err)
	}

	return nil
}

// DisableCoreDumps is included for compatibility reasons. On windows it is a no-op function.
func DisableCoreDumps() error { return nil }

// Auxiliary functions.
func _getPtr(b []byte) uintptr {
	var _p0 unsafe.Pointer
	if len(b) > 0 {
		_p0 = unsafe.Pointer(&b[0])
	} else {
		_p0 = unsafe.Pointer(&_zero)
	}
	return uintptr(_p0)
}

func _getBytes(ptr uintptr, len int, cap int) []byte {
	var sl = struct {
		addr uintptr
		len  int
		cap  int
	}{ptr, len, cap}
	return *(*[]byte)(unsafe.Pointer(&sl))
}
