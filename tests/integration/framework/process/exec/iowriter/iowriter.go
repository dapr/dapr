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

package iowriter

import (
	"bytes"
	"io"
	"sync"
)

// Logger is an interface that provides a Log method and a Name method. The Log
// method is used to write a log message to the logger. The Name method returns
// the name of the logger which will be prepended to each log message.
type Logger interface {
	Log(args ...any)
	Name() string
	Cleanup(func())
}

// stdwriter is an io.WriteCloser that writes to the test logger. It buffers
// writes until a newline is encountered, at which point it flushes the buffer
// to the test logger.
type stdwriter struct {
	t        Logger
	procName string
	buf      bytes.Buffer
	lock     sync.Mutex
}

func New(t Logger, procName string) io.WriteCloser {
	s := &stdwriter{
		t:        t,
		procName: procName,
	}

	t.Cleanup(s.flush)

	return s
}

// Write writes the input bytes to the buffer. If the input contains a newline,
// the buffer is flushed to the test logger.
func (w *stdwriter) Write(inp []byte) (n int, err error) {
	w.lock.Lock()
	defer w.lock.Unlock()
	return w.buf.Write(inp)
}

// Close flushes the buffer and marks the writer as closed.
func (w *stdwriter) Close() error {
	w.flush()
	return nil
}

// flush writes the buffer to the test logger. Expects the lock to be held
// before calling.
func (w *stdwriter) flush() {
	w.lock.Lock()
	defer w.lock.Unlock()
	defer w.buf.Reset()
	for _, line := range bytes.Split(w.buf.Bytes(), []byte("\n")) {
		if len(line) > 0 {
			w.t.Log(w.t.Name() + "/" + w.procName + ": " + string(line))
		}
	}
}
