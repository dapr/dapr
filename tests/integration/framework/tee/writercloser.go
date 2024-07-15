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

package tee

import (
	"errors"
	"io"
)

// writerCloser is a `tee` for multiple write closers.
type writerCloser struct {
	writers []io.WriteCloser
}

// NewWriterCloser creates a new `tee` for io.WriteClosers
func NewWriterCloser(writers ...io.WriteCloser) io.WriteCloser {
	w := new(writerCloser)
	for i := range writers {
		if writers[i] != nil {
			w.writers = append(w.writers, writers[i])
		}
	}
	return w
}

func (w *writerCloser) Write(p []byte) (n int, err error) {
	var errs []error
	for _, writer := range w.writers {
		written, err := writer.Write(p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if written != len(p) {
			return written, errors.New("not all bytes written")
		}
	}

	return len(p), errors.Join(errs...)
}

func (w *writerCloser) Close() error {
	errs := make([]error, len(w.writers))
	for i, writer := range w.writers {
		errs[i] = writer.Close()
	}
	return errors.Join(errs...)
}
