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
	"io"
	"net/http"
)

// NewMultiReaderCloser returns a stream that is like io.MultiReader but that can be closed.
// When the returned stream is closed, it closes the readable streams too, if they implement io.Closer.
func NewMultiReaderCloser(readers ...io.Reader) *MultiReaderCloser {
	r := make([]io.Reader, len(readers))
	copy(r, readers)
	return &MultiReaderCloser{
		readers: r,
	}
}

/*!
Adapted from the Go 1.19.3 source code
Copyright 2009 The Go Authors. All rights reserved.
License: BSD (https://github.com/golang/go/blob/go1.19.3/LICENSE)
*/

// MultiReaderCloser is an io.MultiReader that also implements the io.Closer interface to close the readable streams.
// Readable streams are also closed when we're done reading from them.
type MultiReaderCloser struct {
	readers []io.Reader
}

func (mr *MultiReaderCloser) Read(p []byte) (n int, err error) {
	for len(mr.readers) > 0 {
		r := mr.readers[0]
		n, err = r.Read(p)

		// When reading from a http.Response Body, we may get ErrBodyReadAfterClose if we already read it all
		// We consider that the same as io.EOF
		if errors.Is(err, http.ErrBodyReadAfterClose) {
			err = io.EOF
			mr.readers = mr.readers[1:]
		} else if err == io.EOF {
			if rc, ok := r.(io.Closer); ok {
				_ = rc.Close()
			}
			mr.readers = mr.readers[1:]
		}
		if n > 0 || err != io.EOF {
			if err == io.EOF && len(mr.readers) > 0 {
				// Don't return EOF yet. More readers remain.
				err = nil
			}
			return
		}
	}
	return 0, io.EOF
}

func (mr *MultiReaderCloser) WriteTo(w io.Writer) (sum int64, err error) {
	return mr.writeToWithBuffer(w, make([]byte, 1024*32))
}

func (mr *MultiReaderCloser) writeToWithBuffer(w io.Writer, buf []byte) (sum int64, err error) {
	var n int64
	for i, r := range mr.readers {
		n, err = io.CopyBuffer(w, r, buf)
		sum += n
		if err != nil {
			mr.readers = mr.readers[i:] // permit resume / retry after error
			return sum, err
		}
		mr.readers[i] = nil // permit early GC
	}
	mr.readers = nil
	return sum, nil
}

// Close implements io.Closer.
func (mr *MultiReaderCloser) Close() error {
	for _, r := range mr.readers {
		if rc, ok := r.(io.Closer); ok {
			_ = rc.Close()
		}
	}
	mr.readers = mr.readers[:0]
	return nil
}
