/*
Copyright 2021 The Dapr Authors
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
	"fmt"
	"io"
)

/*!
Adapted from the Go 1.18.3 source code
Copyright 2009 The Go Authors. All rights reserved.
License: BSD (https://github.com/golang/go/blob/go1.18.3/LICENSE)
*/

// TeeReadCloser is like io.TeeReader but returns a stream that can be closed, and which closes the readable stream.
func TeeReadCloser(r io.ReadCloser, w io.Writer) io.ReadCloser {
	return &teeReadCloser{
		r: r,
		w: w,
	}
}

// teeReadCloser is an io.TeeReader that also implements the io.Closer interface to close the readable stream.
type teeReadCloser struct {
	r   io.ReadCloser
	w   io.Writer
	eof bool
}

// Close implements io.Closer.
func (t *teeReadCloser) Close() error {
	var errR, errW error
	errR = t.r.Close()
	// w does not need to implement the io.Closer interface
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

// Read from the R stream and tee it into the w stream.
func (t *teeReadCloser) Read(p []byte) (n int, err error) {
	if t.r == nil || t.w == nil {
		return 0, io.ErrClosedPipe
	}
	if t.eof {
		return 0, io.EOF
	}

	n, err = t.r.Read(p)
	if err == io.EOF {
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
