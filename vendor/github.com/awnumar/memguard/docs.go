/*
Package memguard implements a secure software enclave for the storage of sensitive information in memory.

	package main

	import (
		"fmt"
		"os"

		"github.com/awnumar/memguard"
	)

	func main() {
		// Safely terminate in case of an interrupt signal
		memguard.CatchInterrupt()

		// Purge the session when we return
		defer memguard.Purge()

		// Generate a key sealed inside an encrypted container
		key := memguard.NewEnclaveRandom(32)

		// Passing the key off to another function
		key = invert(key)

		// Decrypt the result returned from invert
		keyBuf, err := key.Open()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
		defer keyBuf.Destroy()

		// Um output it
		fmt.Println(keyBuf.Bytes())
	}

	func invert(key *memguard.Enclave) *memguard.Enclave {
		// Decrypt the key into a local copy
		b, err := key.Open()
		if err != nil {
			memguard.SafePanic(err)
		}
		defer b.Destroy() // Destroy the copy when we return

		// Open returns the data in an immutable buffer, so make it mutable
		b.Melt()

		// Set every element to its complement
		for i := range b.Bytes() {
			b.Bytes()[i] = ^b.Bytes()[i]
		}

		// Return the new data in encrypted form
		return b.Seal() // <- sealing also destroys b
	}

There are two main container objects exposed in this API. Enclave objects encrypt data and store the ciphertext whereas LockedBuffers are more like guarded memory allocations. There is a limit on the maximum number of LockedBuffer objects that can exist at any one time, imposed by the system's mlock limits. There is no limit on Enclaves.

The general workflow is to store sensitive information in Enclaves when it is not immediately needed and decrypt it when and where it is. After use, the LockedBuffer should be destroyed.

If you need access to the data inside a LockedBuffer in a type not covered by any methods provided by this API, you can type-cast the allocation's memory to whatever type you want.

	key := memguard.NewBuffer(32)
	keyArrayPtr := (*[32]byte)(unsafe.Pointer(&key.Bytes()[0])) // do not dereference

This is of course an unsafe operation and so care must be taken to ensure that the cast is valid and does not result in memory unsafety. Further examples of code and interesting use-cases can be found in the examples subpackage.

Several functions exist to make the mass purging of data very easy. It is recommended to make use of them when appropriate.

	// Start an interrupt handler that will clean up memory before exiting
	memguard.CatchInterrupt()

	// Purge the session when returning from the main function of your program
	defer memguard.Purge()

	// Use the safe variants of exit functions provided in the stdlib
	memguard.SafeExit(1)
	memguard.SafePanic(err)

	// Destroy LockedBuffers as soon as possible after using them
	b, err := enclave.Open()
	if err != nil {
		memguard.SafePanic(err)
	}
	defer b.Destroy()

Core dumps are disabled by default. If you absolutely require them, you can enable them by using unix.Setrlimit to set RLIMIT_CORE to an appropriate value.
*/
package memguard
