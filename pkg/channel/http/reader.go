/*
Copyright 2026 The Dapr Authors
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

package http

import (
	"context"
	"io"
	"sync"
)

// cancelOnCloseReader wraps an io.ReadCloser and calls a cancel function
// when Close is called. This is used to cancel the detached h2c request
// context when the pipe reader is closed, ensuring the HTTP/2 goroutine
// does not leak.
type cancelOnCloseReader struct {
	io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
}

func (r *cancelOnCloseReader) Close() error {
	r.once.Do(func() { r.cancel() })
	return r.ReadCloser.Close()
}

// wrapPipeReaderWithCancel wraps a pipe reader so closing it also calls cancel.
// If cancel is nil, the reader is returned as-is.
func wrapPipeReaderWithCancel(pr *io.PipeReader, cancel context.CancelFunc) io.ReadCloser {
	if cancel == nil {
		return pr
	}
	return &cancelOnCloseReader{ReadCloser: pr, cancel: cancel}
}
