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
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/iowriter"
)

// Option is a function that configures the process.
type Option func(*options)

// LogLine is a HTTP server that can be used in integration tests.
type LogLine struct {
	stdout             io.Reader
	stdoutExp          io.WriteCloser
	stdoutLineContains map[string]bool

	stderr            io.Reader
	stderrExp         io.WriteCloser
	stderrLinContains map[string]bool
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
		stdout:             io.TeeReader(stdoutReader, iowriter.New(t, "logline:stdout")),
		stdoutExp:          stdoutWriter,
		stdoutLineContains: stdoutLineContains,
		stderr:             io.TeeReader(stderrReader, iowriter.New(t, "logline:stderr")),
		stderrExp:          stderrWriter,
		stderrLinContains:  stderrLineContains,
	}
}

func (l *LogLine) Run(t *testing.T, ctx context.Context) {
	outCheck := make(chan map[string]bool)
	go func() {
		outCheck <- l.checkOut(t, ctx, l.stdoutLineContains, l.stdoutExp, l.stdout)
	}()
	go func() {
		outCheck <- l.checkOut(t, ctx, l.stderrLinContains, l.stderrExp, l.stderr)
	}()

	for i := 0; i < 2; i++ {
		for expLine := range <-outCheck {
			assert.Fail(t, "expected to log line: %s", expLine)
		}
	}
}

func (l *LogLine) Cleanup(t *testing.T) {
	l.stdoutExp.Close()
	l.stderrExp.Close()
}

func (l *LogLine) checkOut(t *testing.T, ctx context.Context, expLines map[string]bool, closer io.WriteCloser, reader io.Reader) map[string]bool {
	t.Helper()

	closeCh := make(chan struct{})

	go func() {
		select {
		case <-ctx.Done():
		case <-closeCh:
		}
		closer.Close()
	}()

	breader := bufio.NewReader(reader)
	for {
		if len(expLines) == 0 {
			break
		}

		line, _, err := breader.ReadLine()
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
			break
		}
		require.NoError(t, err)

		for expLine := range expLines {
			if strings.Contains(string(line), expLine) {
				delete(expLines, expLine)
			}
		}
	}

	close(closeCh)

	return expLines
}

func (l *LogLine) Stdout() io.WriteCloser {
	return l.stdoutExp
}

func (l *LogLine) Stderr() io.WriteCloser {
	return l.stderrExp
}
