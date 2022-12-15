/*
Copyright 2022 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package streams

import (
	"errors"
	"fmt"
	"io"
	"sync"
)

/*!
Adapted from the Go 1.18.3 source code
Copyright 2009 The Go Authors. All rights reserved.
License: BSD (https://github.com/golang/go/blob/go1.18.3/LICENSE)
*/

// NewTeeReadCloser returns a stream that is like io.TeeReader but that can be closed.
// When the returned stream is closed, it closes the readable stream too, if it implements io.Closer.
func NewTeeReadCloser(r io.Reader, w io.Writer) *TeeReadCloser {
	return &TeeReadCloser{
		r: r,
		w: w,
	}
}

// TeeReadCloser is an io.TeeReader that also implements the io.Closer interface to close the readable stream.
type TeeReadCloser struct {
	r    io.Reader
	w    io.Writer
	eof  bool
	lock sync.Mutex
}

// Close implements io.Closer.
func (t *TeeReadCloser) Close() error {
	t.lock.Lock()
	defer t.lock.Unlock()

	var errR, errW error
	// r and w do not need to implement the io.Closer interface
	if r, ok := t.r.(io.Closer); ok {
		errR = r.Close()
	}
	if w, ok := t.w.(io.Closer); ok {
		errW = w.Close()
	}
	t.r = nil
	t.w = nil

	if errR != nil && errW != nil {
		return fmt.Errorf("failed to close r stream with error='%v' and failed to close w stream with error='%v'", errR, errW)
	} else if errR != nil {
		return fmt.Errorf("failed to close r stream with error='%v'", errR)
	} else if errW != nil {
		return fmt.Errorf("failed to close w stream with error='%v'", errW)
	}
	return nil
}

// Stop closes the underlying writer, which will blocks all future read operations with ErrClosedPipe, but doesn't close the reader stream.
// It's meant to be used when the reader needs to be swapped.
func (t *TeeReadCloser) Stop() (err error) {
	t.lock.Lock()
	defer t.lock.Unlock()

	// w does not need to implement the io.Closer interface
	if w, ok := t.w.(io.Closer); ok {
		err = w.Close()
	}
	t.w = nil

	return err
}

// Read from the R stream and tee it into the w stream.
func (t *TeeReadCloser) Read(p []byte) (n int, err error) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if t.r == nil || t.w == nil {
		return 0, io.ErrClosedPipe
	}
	if t.eof {
		return 0, io.EOF
	}

	n, err = t.r.Read(p)
	if errors.Is(err, io.EOF) {
		t.eof = true
	}
	if n > 0 {
		//nolint:govet
		if n, err := t.w.Write(p[:n]); err != nil {
			return n, err
		}
	}
	return n, err
}
