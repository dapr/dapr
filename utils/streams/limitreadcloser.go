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
	"io"
)

/*!
Adapted from the Go 1.18.3 source code
Copyright 2009 The Go Authors. All rights reserved.
License: BSD (https://github.com/golang/go/blob/go1.18.3/LICENSE)
*/

// LimitReadCloser returns a ReadCloser that reads from r but stops with EOF after n bytes.
func LimitReadCloser(r io.ReadCloser, n int64) io.ReadCloser {
	return &limitReadCloser{
		R: r,
		N: n,
	}
}

type limitReadCloser struct {
	R io.ReadCloser
	N int64
}

func (l *limitReadCloser) Read(p []byte) (n int, err error) {
	if l.N <= 0 || l.R == nil {
		return 0, io.EOF
	}
	if int64(len(p)) > l.N {
		p = p[0:l.N]
	}
	n, err = l.R.Read(p)
	l.N -= int64(n)
	return
}

func (l *limitReadCloser) Close() error {
	return l.R.Close()
}
