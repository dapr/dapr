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
	"fmt"
	"io"
	"strings"
	"sync"
)

// Logger is the subset of *testing.T needed to capture and report test output.
// Output is used rather than Log so that captured lines are not stamped with
// the source location of this package.
type Logger interface {
	Log(args ...any)
	Name() string
	Cleanup(func())
	Failed() bool
	Output() io.Writer
}

// WriteCloser captures the output of a single process. Name reports the label
// the output is reported under, which may differ from the requested name when
// several instances of the same process take part in a test.
type WriteCloser interface {
	io.WriteCloser
	Name() string
}

// stdwriter is an io.WriteCloser which captures process output into the
// collector for a test. Output is buffered and only reported if the test
// fails, or if DAPR_INTEGRATION_LOGS asks for it unconditionally.
type stdwriter struct {
	c *collector
	b *block

	lock sync.Mutex
	buf  bytes.Buffer
}

// New returns a writer which captures the output of procName for t. Calling it
// more than once with the same name for the same test appends an index to the
// name, so that multiple instances of a process stay distinguishable.
func New(t Logger, procName string) WriteCloser {
	c := collectorFor(t)
	w := &stdwriter{c: c, b: c.newBlock(procName)}
	c.addWriter(w)

	return w
}

// Name returns the label this writer's output is reported under.
func (w *stdwriter) Name() string {
	return w.b.name
}

// Eventf records something the framework did, as opposed to something a process
// printed. Events share the report of the process output they interleave with,
// and like it are only shown when the test fails.
func Eventf(t Logger, format string, args ...any) {
	c := collectorFor(t)
	c.events.append(c.since(), fmt.Sprintf(format, args...))
}

// Notef records a message shown at the top of the report. Reserve it for
// conditions which explain an entire failure, such as a blown deadline.
func Notef(t Logger, format string, args ...any) {
	collectorFor(t).note(fmt.Sprintf(format, args...))
}

// Write captures the input, timestamping each complete line as it arrives.
func (w *stdwriter) Write(inp []byte) (int, error) {
	w.lock.Lock()
	defer w.lock.Unlock()

	n, err := w.buf.Write(inp)
	w.drain(false)

	return n, err
}

// Close captures any trailing line which was not newline terminated. It is
// safe to call more than once.
func (w *stdwriter) Close() error {
	w.lock.Lock()
	defer w.lock.Unlock()
	w.drain(true)

	return nil
}

// drain moves whole lines from the buffer into the block. When final is set,
// a trailing line without a newline is moved too.
func (w *stdwriter) drain(final bool) {
	at := w.c.since()

	for {
		i := bytes.IndexByte(w.buf.Bytes(), '\n')
		if i < 0 {
			break
		}
		text := string(w.buf.Next(i + 1)[:i])
		w.b.append(at, strings.TrimSuffix(text, "\r"))
	}

	if final && w.buf.Len() > 0 {
		w.b.append(at, strings.TrimSuffix(w.buf.String(), "\r"))
		w.buf.Reset()
	}
}
