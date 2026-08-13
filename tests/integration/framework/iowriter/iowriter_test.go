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
	stdlog "log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/kit/logger"
)

func init() {
	// Keep expectations stable regardless of the terminal the tests run in.
	useColor = false
	useUnicode = false
}

func TestMain(m *testing.M) {
	// Keep these tests out of the directory a real suite run writes to.
	dir, err := os.MkdirTemp("", "iowriter-test")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	os.Setenv("DAPR_INTEGRATION_LOGS_DIR", dir)

	m.Run()
}

type mockLogger struct {
	t      *testing.T
	name   string
	failed bool
	out    bytes.Buffer
	msgs   []string
}

func (m *mockLogger) Log(args ...any) {
	m.msgs = append(m.msgs, args[0].(string))
}

func (m *mockLogger) Name() string {
	if m.name != "" {
		return m.name
	}
	return "TestLogger"
}

func (m *mockLogger) Cleanup(fn func()) {
	m.t.Cleanup(fn)
}

func (m *mockLogger) Failed() bool {
	return m.failed
}

func (m *mockLogger) Output() io.Writer {
	return &m.out
}

// report runs fn as a subtest and returns the report it produced, following the
// pointer line to the log file when one was written.
func report(t *testing.T, logger *mockLogger, fn func()) string {
	t.Helper()

	t.Setenv("DAPR_INTEGRATION_LOGS_DIR", t.TempDir())

	t.Run("test", func(t *testing.T) {
		logger.t = t
		fn()
	})

	out := logger.out.String()
	path, ok := strings.CutPrefix(strings.TrimSpace(out), "logs: ")
	if !ok {
		return out
	}

	b, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(b)
}

func TestNew(t *testing.T) {
	t.Run("should return new stdwriter", func(t *testing.T) {
		writer := New(&mockLogger{t: t}, "proc")
		_, ok := writer.(*stdwriter)
		assert.True(t, ok)
	})

	t.Run("should report captured lines on failure", func(t *testing.T) {
		logger := &mockLogger{failed: true}
		out := report(t, logger, func() {
			fmt.Fprint(New(logger, "proc"), "hello\nworld\n")
		})

		assert.Contains(t, out, "-- proc --")
		assert.Contains(t, out, "hello")
		assert.Contains(t, out, "world")
		assert.Contains(t, out, "FAIL")
	})

	t.Run("should point at the log file rather than inline the report", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("DAPR_INTEGRATION_LOGS_DIR", dir)

		logger := &mockLogger{failed: true, name: "Test_Integration/ports/Ports"}
		t.Run("test", func(t *testing.T) {
			logger.t = t
			fmt.Fprint(New(logger, "proc"), "hello\n")
		})

		want := filepath.Join(dir, "ports.Ports.log")
		assert.Equal(t, "logs: "+want+"\n", logger.out.String())
		assert.FileExists(t, want)
	})

	t.Run("should inline the report when asked", func(t *testing.T) {
		logger := &mockLogger{failed: true, name: "Test_Integration/inlined/case"}
		t.Run("test", func(t *testing.T) {
			t.Setenv("DAPR_INTEGRATION_LOGS_INLINE", "true")
			logger.t = t
			fmt.Fprint(New(logger, "proc"), "hello\n")
		})

		assert.Contains(t, logger.out.String(), "-- proc --")
		assert.Contains(t, logger.out.String(), "hello")

		// Still listed in the end of run summary, just without a file to read.
		assert.Contains(t, Failures(), Failure{Test: "inlined/case", Path: ""})
	})

	t.Run("should report captured lines when always logging", func(t *testing.T) {
		logger := &mockLogger{}
		out := report(t, logger, func() {
			t.Setenv("DAPR_INTEGRATION_LOGS", "true")
			fmt.Fprint(New(logger, "proc"), "hello\n")
		})

		assert.Contains(t, out, "hello")
		assert.Contains(t, out, "PASS")
	})

	t.Run("should report nothing by default", func(t *testing.T) {
		logger := &mockLogger{}
		t.Run("test", func(t *testing.T) {
			logger.t = t
			fmt.Fprint(New(logger, "proc"), "hello\n")
		})

		assert.Empty(t, logger.out.String())
		assert.Empty(t, logger.msgs)
	})

	t.Run("should index repeated process names", func(t *testing.T) {
		logger := &mockLogger{failed: true}
		out := report(t, logger, func() {
			for i := range 3 {
				fmt.Fprintf(New(logger, "daprd"), "line %d\n", i)
			}
		})

		assert.Contains(t, out, "-- daprd --")
		assert.Contains(t, out, "-- daprd-1 --")
		assert.Contains(t, out, "-- daprd-2 --")
	})

	t.Run("should share one report between processes and events", func(t *testing.T) {
		logger := &mockLogger{failed: true}
		out := report(t, logger, func() {
			Eventf(logger, "starting %d processes", 2)
			Notef(logger, "context deadline exceeded")
			fmt.Fprint(New(logger, "daprd"), "from daprd\n")
		})

		assert.Equal(t, 1, strings.Count(out, "logs:"), out)
		assert.Contains(t, out, "-- framework --")
		assert.Contains(t, out, "starting 2 processes")
		assert.Contains(t, out, "context deadline exceeded")
		assert.Contains(t, out, "from daprd")
	})

	t.Run("should strip the suite prefix from the report name", func(t *testing.T) {
		logger := &mockLogger{failed: true, name: "Test_Integration/ports/Ports"}
		assert.Contains(t, report(t, logger, func() {
			fmt.Fprint(New(logger, "proc"), "hi\n")
		}), "logs: ports/Ports ")
	})

	t.Run("should record failures with their log path", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("DAPR_INTEGRATION_LOGS_DIR", dir)

		logger := &mockLogger{failed: true, name: "Test_Integration/actors/call/non200"}
		t.Run("test", func(t *testing.T) {
			logger.t = t
			fmt.Fprint(New(logger, "proc"), "boom\n")
		})

		assert.Contains(t, Failures(), Failure{
			Test: "actors/call/non200",
			Path: filepath.Join(dir, "actors.call.non200.log"),
		})
	})

	t.Run("should not record a passing test as a failure", func(t *testing.T) {
		logger := &mockLogger{name: "Test_Integration/passing/case"}
		report(t, logger, func() {
			t.Setenv("DAPR_INTEGRATION_LOGS", "true")
			fmt.Fprint(New(logger, "proc"), "fine\n")
		})

		for _, f := range Failures() {
			assert.NotEqual(t, "passing/case", f.Test)
		}
	})
}

func TestInline(t *testing.T) {
	t.Run("should write files by default", func(t *testing.T) {
		t.Setenv("GITHUB_ACTIONS", "")
		assert.False(t, inline())
	})

	t.Run("should inline on GitHub Actions, where output is grouped", func(t *testing.T) {
		t.Setenv("GITHUB_ACTIONS", "true")
		assert.True(t, inline())
	})

	t.Run("should let the env var win either way", func(t *testing.T) {
		t.Setenv("GITHUB_ACTIONS", "true")
		t.Setenv("DAPR_INTEGRATION_LOGS_INLINE", "false")
		assert.False(t, inline())

		t.Setenv("GITHUB_ACTIONS", "")
		t.Setenv("DAPR_INTEGRATION_LOGS_INLINE", "true")
		assert.True(t, inline())
	})
}

func TestStripANSI(t *testing.T) {
	tests := map[string]string{
		"":                          "",
		"plain":                     "plain",
		"\x1b[31mred\x1b[0m":        "red",
		"\x1b[2m\x1b[1mboth\x1b[0m": "both",
		"a\x1b[33mb\x1b[0mc":        "abc",
	}

	for in, exp := range tests {
		assert.Equal(t, exp, stripANSI(in), in)
	}
}

func TestLogFileName(t *testing.T) {
	tests := map[string]string{
		"ports/Ports":         "ports.Ports.log",
		"actors/call/non200":  "actors.call.non200.log",
		"a b/c":               "a.b.c.log",
		"weird:name*with?bad": "weird.name.with.bad.log",
	}

	for in, exp := range tests {
		assert.Equal(t, exp, logFileName(in), in)
	}
}

func TestWrite(t *testing.T) {
	t.Run("should buffer until a newline arrives", func(t *testing.T) {
		logger := &mockLogger{t: t, failed: true}
		writer := New(logger, "proc").(*stdwriter)

		_, err := writer.Write([]byte("par"))
		require.NoError(t, err)
		assert.Empty(t, writer.b.lines)

		_, err = writer.Write([]byte("tial\n"))
		require.NoError(t, err)
		require.Len(t, writer.b.lines, 1)
		assert.Equal(t, "partial", writer.b.lines[0].text)
	})

	t.Run("should capture a trailing line on close", func(t *testing.T) {
		logger := &mockLogger{t: t, failed: true}
		writer := New(logger, "proc").(*stdwriter)

		_, err := writer.Write([]byte("no newline"))
		require.NoError(t, err)
		assert.Empty(t, writer.b.lines)

		require.NoError(t, writer.Close())
		require.Len(t, writer.b.lines, 1)
		assert.Equal(t, "no newline", writer.b.lines[0].text)
	})

	t.Run("should be idempotent on close", func(t *testing.T) {
		logger := &mockLogger{t: t, failed: true}
		writer := New(logger, "proc").(*stdwriter)

		_, err := writer.Write([]byte("line"))
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		require.NoError(t, writer.Close())

		assert.Len(t, writer.b.lines, 1)
	})

	t.Run("should strip carriage returns", func(t *testing.T) {
		logger := &mockLogger{t: t, failed: true}
		writer := New(logger, "proc").(*stdwriter)

		_, err := writer.Write([]byte("windows\r\n"))
		require.NoError(t, err)
		require.Len(t, writer.b.lines, 1)
		assert.Equal(t, "windows", writer.b.lines[0].text)
	})
}

func TestConcurrency(t *testing.T) {
	t.Run("should handle concurrent writes", func(t *testing.T) {
		logger := &mockLogger{t: t, failed: true}
		writer := New(logger, "proc").(*stdwriter)

		var wg sync.WaitGroup
		wg.Add(2)
		for range 2 {
			go func() {
				defer wg.Done()
				for i := range 1000 {
					fmt.Fprintf(writer, "test %d\n", i)
				}
			}()
		}
		wg.Wait()

		require.NoError(t, writer.Close())
		assert.Len(t, writer.b.lines, 2000)
		for _, l := range writer.b.lines {
			assert.Contains(t, l.text, "test ")
		}
	})

	t.Run("should handle concurrent process registration", func(t *testing.T) {
		logger := &mockLogger{t: t, failed: false}

		var wg sync.WaitGroup
		wg.Add(10)
		for range 10 {
			go func() {
				defer wg.Done()
				fmt.Fprint(New(logger, "daprd"), "line\n")
			}()
		}
		wg.Wait()

		c := collectorFor(logger)
		assert.Len(t, c.blocks, 10)
		assert.Equal(t, 10, c.names["daprd"])
	})
}

func TestRedirectInProcessLogs(t *testing.T) {
	t.Run("should send package logs to a file", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("DAPR_INTEGRATION_LOGS_DIR", dir)

		// Registered before the redirect, the way a package level logger such as
		// the one in pkg/security is registered at init.
		log := logger.NewLogger("test.inprocess.scope")

		path, err := RedirectInProcessLogs()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(dir, inProcessLog), path)

		log.Info("not for the terminal")

		b, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Contains(t, string(b), "not for the terminal")
	})

	t.Run("should send standard library logs to the same file", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("DAPR_INTEGRATION_LOGS_DIR", dir)

		path, err := RedirectInProcessLogs()
		require.NoError(t, err)

		// net/http reports things like TLS handshake errors through this logger
		// whenever a server has no ErrorLog of its own.
		stdlog.Printf("http: TLS handshake error from 127.0.0.1:41900")

		b, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Contains(t, string(b), "TLS handshake error")
	})

	t.Run("should leave logs alone when asked", func(t *testing.T) {
		t.Setenv("DAPR_INTEGRATION_LOGS_DIR", t.TempDir())
		t.Setenv("DAPR_INTEGRATION_INPROCESS_LOGS", "true")

		path, err := RedirectInProcessLogs()
		require.NoError(t, err)
		assert.Empty(t, path)
	})
}
