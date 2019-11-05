package memguard

import (
	"github.com/awnumar/memguard/core"
)

/*
Enclave is a sealed and encrypted container for sensitive data.
*/
type Enclave struct {
	*core.Enclave
}

/*
NewEnclave seals up some data into an encrypted enclave object. The buffer is wiped after the data is copied. If the length of the buffer is zero, the function will return nil.

A LockedBuffer may alternatively be converted into an Enclave object using its Seal method. This will also have the effect of destroying the LockedBuffer.
*/
func NewEnclave(src []byte) *Enclave {
	e, err := core.NewEnclave(src)
	if err != nil {
		if err == core.ErrNullEnclave {
			return nil
		}
		core.Panic(err)
	}
	return &Enclave{e}
}

/*
NewEnclaveRandom generates and seals arbitrary amounts of cryptographically-secure random bytes into an encrypted enclave object. If size is not strictly positive the function will return nil.
*/
func NewEnclaveRandom(size int) *Enclave {
	// todo: stream data into enclave
	b := NewBufferRandom(size)
	return b.Seal()
}

/*
Open decrypts an Enclave object and places its contents into an immutable LockedBuffer. An error will be returned if decryption failed.
*/
func (e *Enclave) Open() (*LockedBuffer, error) {
	b, err := core.Open(e.Enclave)
	if err != nil {
		if err != core.ErrDecryptionFailed {
			core.Panic(err)
		}
		return nil, err
	}
	b.Freeze()
	return newBuffer(b), nil
}

/*
Size returns the number of bytes of data stored within an Enclave.
*/
func (e *Enclave) Size() int {
	return core.EnclaveSize(e.Enclave)
}
