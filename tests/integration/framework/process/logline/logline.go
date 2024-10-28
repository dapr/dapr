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

package logline

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Option is a function that configures the process.
type Option func(*options)

type LogLine struct {
	stdout             io.ReadCloser
	stdoutExp          io.WriteCloser
	stdoutLineContains map[string]bool

	stderr            io.ReadCloser
	stderrExp         io.WriteCloser
	stderrLinContains map[string]bool
	got               bytes.Buffer

	outCheck chan map[string]bool
	closeCh  chan struct{}
	done     atomic.Int32
}

func New(t *testing.T, fopts ...Option) *LogLine {
	t.Helper()

	opts := options{}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	stdoutLineContains := make(map[string]bool)
	for _, stdout := range opts.stdoutContains {
		stdoutLineContains[stdout] = false
	}

	stderrLineContains := make(map[string]bool)
	for _, stderr := range opts.stderrContains {
		stderrLineContains[stderr] = false
	}

	stdoutReader, stdoutWriter := io.Pipe()
	stderrReader, stderrWriter := io.Pipe()

	return &LogLine{
		stdout:             stdoutReader,
		stdoutExp:          stdoutWriter,
		stdoutLineContains: stdoutLineContains,
		stderr:             stderrReader,
		stderrExp:          stderrWriter,
		stderrLinContains:  stderrLineContains,
		outCheck:           make(chan map[string]bool),
		closeCh:            make(chan struct{}),
	}
}

func (l *LogLine) Run(t *testing.T, ctx context.Context) {
	go func() {
		res := l.checkOut(t, ctx, l.stdoutLineContains, l.stdoutExp, l.stdout)
		l.outCheck <- res
	}()
	go func() {
		res := l.checkOut(t, ctx, l.stderrLinContains, l.stderrExp, l.stderr)
		l.outCheck <- res
	}()
}

func (l *LogLine) FoundAll() bool {
	return l.done.Load() == 2
}

func (l *LogLine) Cleanup(t *testing.T) {
	close(l.closeCh)
	for range 2 {
		for expLine := range <-l.outCheck {
			assert.Fail(t, "expected to log line: "+expLine, l.got.String())
		}
	}
}

func (l *LogLine) checkOut(t *testing.T, ctx context.Context, expLines map[string]bool, closer io.WriteCloser, reader io.Reader) map[string]bool {
	t.Helper()

	if len(expLines) == 0 {
		go io.Copy(io.Discard, reader)
		l.done.Add(1)
		return expLines
	}

	go func() {
		select {
		case <-ctx.Done():
			closer.Close()
		case <-l.closeCh:
		}
	}()

	var once sync.Once

	breader := bufio.NewReader(reader)
	for {
		if len(expLines) == 0 {
			once.Do(func() { l.done.Add(1) })
		}

		line, _, err := breader.ReadLine()
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
			break
		}
		//nolint:testifylint
		assert.NoError(t, err)

		l.got.Write(append(line, '\n'))

		for expLine := range expLines {
			if strings.Contains(string(line), expLine) {
				delete(expLines, expLine)
			}
		}
	}

	return expLines
}

func (l *LogLine) Stdout() io.WriteCloser {
	return l.stdoutExp
}

func (l *LogLine) Stderr() io.WriteCloser {
	return l.stderrExp
}

func (l *LogLine) EventuallyFoundAll(t *testing.T) {
	assert.Eventually(t, l.FoundAll, time.Second*15, time.Millisecond*10)
}
