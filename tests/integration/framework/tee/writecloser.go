/*
Copyright 2024 The Dapr Authors
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
	"sync"
)

// writecloser is a `tee` for multiple write closers.
type writecloser struct {
	lock    sync.Mutex
	writers []io.WriteCloser
}

// WriteCloser creates a new `tee` for io.WriteClosers.
func WriteCloser(writers ...io.WriteCloser) io.WriteCloser {
	w := new(writecloser)
	for i := range writers {
		if writers[i] != nil {
			w.writers = append(w.writers, writers[i])
		}
	}
	return w
}

func (w *writecloser) Write(p []byte) (n int, err error) {
	w.lock.Lock()
	defer w.lock.Unlock()

	var errs []error

	for _, writer := range w.writers {
		var n int
		for {
			written, err := writer.Write(p)
			if err != nil {
				errs = append(errs, err)
				break
			}

			// Keep writing until all bytes are written to this writer.
			n += written
			if n == len(p) {
				break
			}
		}
	}

	if err := errors.Join(errs...); err != nil {
		return 0, err
	}

	return len(p), nil
}

func (w *writecloser) Close() error {
	w.lock.Lock()
	defer w.lock.Unlock()

	errs := make([]error, len(w.writers))
	for i, writer := range w.writers {
		errs[i] = writer.Close()
	}

	return errors.Join(errs...)
}
