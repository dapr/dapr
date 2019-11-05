package core

import (
	"errors"
	"sync"
	"time"
)

// Interval of time between each verify & re-key cycle.
const Interval = 500 // milliseconds

// ErrCofferExpired is returned when a function attempts to perform an operation using a secure key container that has been wiped and destroyed.
var ErrCofferExpired = errors.New("<memguard::core::ErrCofferExpired> attempted usage of destroyed key object")

/*
Coffer is a specialized container for securing highly-sensitive, 32 byte values.
*/
type Coffer struct {
	sync.RWMutex

	left  *Buffer // Left partition.
	right *Buffer // Right partition.

	rand *Buffer // Static allocation for fast random bytes reading.
}

// NewCoffer is a raw constructor for the *Coffer object.
func NewCoffer() *Coffer {
	// Create a new Coffer object.
	s := new(Coffer)

	// Allocate the partitions.
	s.left, _ = NewBuffer(32)
	s.right, _ = NewBuffer(32)
	s.rand, _ = NewBuffer(32)

	// Initialise with a random 32 byte value.
	s.Initialise()

	go func(s *Coffer) {
		for {
			// Sleep for the specified interval.
			time.Sleep(Interval * time.Millisecond)

			// Re-key the contents, exiting the routine if object destroyed.
			if err := s.Rekey(); err != nil {
				break
			}
		}
	}(s)

	return s
}

/*
Initialise is used to reset the value stored inside a Coffer to a new random 32 byte value, overwriting the old.
*/
func (s *Coffer) Initialise() error {
	// Check if it has been destroyed.
	if s.Destroyed() {
		return ErrCofferExpired
	}

	// Attain the mutex.
	s.Lock()
	defer s.Unlock()

	// Overwrite the old value with fresh random bytes.
	Scramble(s.left.Data())
	Scramble(s.right.Data())

	// left = left XOR hash(right)
	hr := Hash(s.right.Data())
	for i := range hr {
		s.left.Data()[i] ^= hr[i]
	}
	Wipe(hr)

	return nil
}

/*
View returns a snapshot of the contents of a Coffer inside a Buffer. As usual the Buffer should be destroyed as soon as possible after use by calling the Destroy method.
*/
func (s *Coffer) View() (*Buffer, error) {
	// Attain a read-only lock.
	s.RLock()
	defer s.RUnlock()

	// Check if it's destroyed.
	if s.Destroyed() {
		return nil, ErrCofferExpired
	}

	// Create a new Buffer for the data.
	b, _ := NewBuffer(32)

	// data = hash(right) XOR left
	h := Hash(s.right.Data())
	for i := range b.Data() {
		b.Data()[i] = h[i] ^ s.left.Data()[i]
	}
	Wipe(h)

	// Return the view.
	return b, nil
}

/*
Rekey is used to re-key a Coffer. Ideally this should be done at short, regular intervals.
*/
func (s *Coffer) Rekey() error {
	// Check if it has been destroyed.
	if s.Destroyed() {
		return ErrCofferExpired
	}

	// Attain the mutex.
	s.Lock()
	defer s.Unlock()

	// Attain 32 bytes of fresh cryptographic buf32.
	Scramble(s.rand.Data())

	// Hash the current right partition for later.
	hashRightCurrent := Hash(s.right.Data())

	// new_right = current_right XOR buf32
	for i := range s.right.Data() {
		s.right.Data()[i] ^= s.rand.Data()[i]
	}

	// new_left = current_left XOR hash(current_right) XOR hash(new_right)
	hashRightNew := Hash(s.right.Data())
	for i := range s.left.Data() {
		s.left.Data()[i] ^= hashRightCurrent[i] ^ hashRightNew[i]
	}
	Wipe(hashRightNew)

	return nil
}

/*
Destroy wipes and cleans up all memory related to a Coffer object. Once this method has been called, the Coffer can no longer be used and a new one should be created instead.
*/
func (s *Coffer) Destroy() {
	// Attain the mutex.
	s.Lock()
	defer s.Unlock()

	// Destroy the partitions.
	s.left.Destroy()
	s.right.Destroy()
	s.rand.Destroy()
}

// Destroyed returns a boolean value indicating if a Coffer has been destroyed.
func (s *Coffer) Destroyed() bool {
	s.RLock()
	defer s.RUnlock()

	return (!s.left.Alive()) && (!s.right.Alive())
}
