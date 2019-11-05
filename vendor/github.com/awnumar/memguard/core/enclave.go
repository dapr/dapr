package core

import "errors"

var (
	// Declare a key for use in encrypting data this session.
	key *Coffer
)

func init() {
	// Initialize the key declared above with a random value.
	if key == nil {
		key = NewCoffer()
	}
}

// ErrNullEnclave is returned when attempting to construct an enclave of size less than one.
var ErrNullEnclave = errors.New("<memguard::core::ErrNullEnclave> enclave size must be greater than zero")

/*
Enclave is a sealed and encrypted container for sensitive data.
*/
type Enclave struct {
	ciphertext []byte
}

/*
NewEnclave is a raw constructor for the Enclave object. The given buffer is wiped after the enclave is created.
*/
func NewEnclave(buf []byte) (*Enclave, error) {
	// Return an error if length < 1.
	if len(buf) < 1 {
		return nil, ErrNullEnclave
	}

	// Create a new Enclave.
	e := new(Enclave)

	// Get a view of the key.
	k, err := key.View()
	if err != nil {
		return nil, err
	}

	// Encrypt the plaintext.
	e.ciphertext, err = Encrypt(buf, k.Data())
	if err != nil {
		Panic(err) // key is not 32 bytes long
	}

	// Destroy our copy of the key.
	k.Destroy()

	// Wipe the given buffer.
	Wipe(buf)

	return e, nil
}

/*
Seal consumes a given Buffer object and returns its data secured and encrypted inside an Enclave. The given Buffer is destroyed after the Enclave is created.
*/
func Seal(b *Buffer) (*Enclave, error) {
	// Check if the Buffer has been destroyed.
	if !b.Alive() {
		return nil, ErrBufferExpired
	}

	b.Melt()  // Make the buffer mutable so that we can wipe it.
	b.RLock() // Attain a read lock.

	// Construct the Enclave from the Buffer's data.
	e, err := NewEnclave(b.Data())
	if err != nil {
		return nil, err
	}

	// Destroy the Buffer object.
	b.RUnlock()
	b.Destroy()

	// Return the newly created Enclave.
	return e, nil
}

/*
Open decrypts an Enclave and puts the contents into a Buffer object. The given Enclave is left untouched and may be reused.

The Buffer object should be destroyed after the contents are no longer needed.
*/
func Open(e *Enclave) (*Buffer, error) {
	// Allocate a secure Buffer to hold the decrypted data.
	b, err := NewBuffer(len(e.ciphertext) - Overhead)
	if err != nil {
		Panic("<memguard:core> ciphertext has invalid length") // ciphertext has invalid length
	}

	// Grab a view of the key.
	k, err := key.View()
	if err != nil {
		return nil, err
	}

	// Decrypt the enclave into the buffer we created.
	_, err = Decrypt(e.ciphertext, k.Data(), b.Data())
	if err != nil {
		return nil, err
	}

	// Destroy our copy of the key.
	k.Destroy()

	// Return the contents of the Enclave inside a Buffer.
	return b, nil
}

/*
EnclaveSize returns the number of bytes of plaintext data stored inside an Enclave.
*/
func EnclaveSize(e *Enclave) int {
	return len(e.ciphertext) - Overhead
}
