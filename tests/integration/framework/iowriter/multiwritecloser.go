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

package iowriter

import (
	"errors"
	"io"
)

// multiWriteCloser fans writes out to multiple WriteClosers, e.g. to attach
// multiple logline watchers to a single process output.
type multiWriteCloser struct {
	ws []io.WriteCloser
}

// NewMultiWriteCloser returns an io.WriteCloser which duplicates its writes
// to all the provided WriteClosers, and closes them all on Close.
func NewMultiWriteCloser(ws ...io.WriteCloser) io.WriteCloser {
	return multiWriteCloser{ws: ws}
}

func (m multiWriteCloser) Write(p []byte) (int, error) {
	for _, w := range m.ws {
		if _, err := w.Write(p); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (m multiWriteCloser) Close() error {
	errs := make([]error, 0, len(m.ws))
	for _, w := range m.ws {
		errs = append(errs, w.Close())
	}
	return errors.Join(errs...)
}
