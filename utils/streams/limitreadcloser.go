/*
Copyright 2023 The Dapr Authors
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
)

/*!
Adapted from the Go 1.18.3 source code
Copyright 2009 The Go Authors. All rights reserved.
License: BSD (https://github.com/golang/go/blob/go1.18.3/LICENSE)
*/

// ErrStreamTooLarge is returned by LimitReadCloser when the stream is too large.
var ErrStreamTooLarge = errors.New("stream too large")

// LimitReadCloser returns a ReadCloser that reads from r but stops with ErrStreamTooLarge after n bytes.
func LimitReadCloser(r io.ReadCloser, n int64) io.ReadCloser {
	return &limitReadCloser{
		R: r,
		N: n,
	}
}

type limitReadCloser struct {
	R      io.ReadCloser
	N      int64
	closed bool
}

func (l *limitReadCloser) Read(p []byte) (n int, err error) {
	if l.N < 0 || l.R == nil {
		return 0, ErrStreamTooLarge
	}
	if len(p) == 0 {
		return 0, nil
	}
	if l.closed {
		return 0, io.EOF
	}
	if int64(len(p)) > (l.N + 1) {
		p = p[0:(l.N + 1)]
	}
	n, err = l.R.Read(p)
	l.N -= int64(n)
	if l.N < 0 {
		// Special case if we just read the "l.N+1" byte
		if l.N == -1 {
			n--
		}
		if err == nil {
			err = ErrStreamTooLarge
		}
		if !l.closed {
			l.closed = true
			l.R.Close()
		}
	}
	return
}

func (l *limitReadCloser) Close() error {
	if l.closed {
		return nil
	}
	l.closed = true
	return l.R.Close()
}
