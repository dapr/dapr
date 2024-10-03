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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockLogger struct {
	msgs   []string
	failed bool
	t      *testing.T
}

func (m *mockLogger) Log(args ...any) {
	m.msgs = append(m.msgs, args[0].(string))
}

func (m mockLogger) Name() string {
	return "TestLogger"
}

func (m mockLogger) Cleanup(fn func()) {
	m.t.Cleanup(fn)
}

func (m mockLogger) Failed() bool {
	return m.failed
}

func TestNew(t *testing.T) {
	t.Run("should return new stdwriter", func(t *testing.T) {
		writer := New(&mockLogger{t: t}, "proc")
		_, ok := writer.(*stdwriter)
		assert.True(t, ok)
	})

	var buf bytes.Buffer
	logger := &mockLogger{failed: true}
	t.Run("should flush on Cleanup (failed)", func(t *testing.T) {
		logger.t = t
		writer := New(logger, "proc").(*stdwriter)
		writer.buf = buf
		writer.Write([]byte("test"))
		assert.Equal(t, "test", writer.buf.String())
	})
	_ = assert.Len(t, logger.msgs, 1) &&
		assert.Equal(t, "TestLogger/proc: test", logger.msgs[0])
	assert.Empty(t, buf.Len())

	buf = bytes.Buffer{}
	logger = &mockLogger{failed: false}
	t.Run("should flush on Cleanup (env)", func(t *testing.T) {
		t.Setenv("DAPR_INTEGRATION_LOGS", "true")
		logger.t = t
		writer := New(logger, "proc").(*stdwriter)
		writer.buf = buf
		writer.Write([]byte("test"))
		assert.Equal(t, "test", writer.buf.String())
	})
	_ = assert.Len(t, logger.msgs, 1) &&
		assert.Equal(t, "TestLogger/proc: test", logger.msgs[0])
	assert.Empty(t, buf.Len())

	buf = bytes.Buffer{}
	logger = &mockLogger{failed: false}
	t.Run("should not flush on Cleanup by default", func(t *testing.T) {
		logger.t = t
		writer := New(logger, "proc").(*stdwriter)
		writer.buf = buf
		writer.Write([]byte("test"))
		assert.Equal(t, "test", writer.buf.String())
	})
	assert.Empty(t, logger.msgs)
	assert.Empty(t, buf.Len())
}

func TestWrite(t *testing.T) {
	t.Run("should write to buffer", func(t *testing.T) {
		logger := &mockLogger{t: t, failed: true}
		writer := New(logger, "proc").(*stdwriter)

		_, err := writer.Write([]byte("test"))
		require.NoError(t, err)
		assert.Equal(t, "test", writer.buf.String())
	})

	t.Run("should not flush on newline or on close", func(t *testing.T) {
		logger := &mockLogger{t: t, failed: true}
		writer := New(logger, "proc").(*stdwriter)

		_, err := writer.Write([]byte("test\n"))
		require.NoError(t, err)

		assert.Equal(t, 5, writer.buf.Len())

		assert.Empty(t, logger.msgs)

		require.NoError(t, writer.Close())

		assert.Equal(t, 5, writer.buf.Len())
	})

	t.Run("should not return error on write when closed", func(t *testing.T) {
		writer := New(&mockLogger{t: t}, "proc").(*stdwriter)
		require.NoError(t, writer.Close())

		_, err := writer.Write([]byte("test\n"))
		require.NotErrorIs(t, err, io.ErrClosedPipe)
		assert.Equal(t, "test\n", writer.buf.String())
	})
}

func TestClose(t *testing.T) {
	t.Run("should be a noop", func(t *testing.T) {
		logger := &mockLogger{t: t, failed: true}
		writer := New(logger, "proc").(*stdwriter)
		writer.Write([]byte("test"))
		writer.Close()

		assert.Equal(t, 4, writer.buf.Len())
	})
}

func TestConcurrency(t *testing.T) {
	t.Run("should handle concurrent writes", func(t *testing.T) {
		logger := &mockLogger{t: t, failed: true}
		writer := New(logger, "proc").(*stdwriter)
		wg := sync.WaitGroup{}
		wg.Add(2)

		go func() {
			defer wg.Done()
			for i := range 1000 {
				fmt.Fprintf(writer, "test %d\n", i)
			}
		}()

		go func() {
			defer wg.Done()
			for i := range 1000 {
				fmt.Fprintf(writer, "test %d\n", i)
			}
		}()

		wg.Wait()

		require.NoError(t, writer.Close())

		writer.flush()
		assert.Equal(t, 0, writer.buf.Len())
		assert.Len(t, logger.msgs, 2000)

		for _, msg := range logger.msgs {
			assert.Contains(t, msg, "TestLogger/proc: test ")
		}
	})
}
