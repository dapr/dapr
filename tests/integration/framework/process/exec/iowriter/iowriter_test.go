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
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockLogger struct {
	msgs []string
}

func (m *mockLogger) Log(args ...any) {
	m.msgs = append(m.msgs, args[0].(string))
}

func (m mockLogger) Name() string {
	return "TestLogger"
}

func TestNew(t *testing.T) {
	t.Run("should return new stdwriter", func(t *testing.T) {
		writer := New(new(mockLogger), "proc")
		_, ok := writer.(*stdwriter)
		assert.True(t, ok)
	})
}

func TestWrite(t *testing.T) {
	t.Run("should write to buffer", func(t *testing.T) {
		logger := new(mockLogger)
		writer := New(logger, "proc").(*stdwriter)

		_, err := writer.Write([]byte("test"))
		require.NoError(t, err)
		assert.Equal(t, "test", writer.buf.String())
	})

	t.Run("should flush on newline", func(t *testing.T) {
		logger := new(mockLogger)
		writer := New(logger, "proc").(*stdwriter)

		_, err := writer.Write([]byte("test\n"))
		require.NoError(t, err)

		assert.Equal(t, 0, writer.buf.Len())

		_ = assert.Len(t, logger.msgs, 1) && assert.Equal(t, "TestLogger/proc: test", logger.msgs[0])
	})

	t.Run("should return error when closed", func(t *testing.T) {
		writer := New(new(mockLogger), "proc").(*stdwriter)
		writer.Close()

		_, err := writer.Write([]byte("test\n"))
		assert.NoError(t, err)
		assert.Empty(t, writer.buf.String())
	})
}

func TestClose(t *testing.T) {
	t.Run("should flush and close", func(t *testing.T) {
		logger := new(mockLogger)
		writer := New(logger, "proc").(*stdwriter)
		writer.Write([]byte("test"))
		writer.Close()

		assert.Equal(t, 0, writer.buf.Len())
		_ = assert.Equal(t, 1, len(logger.msgs)) && assert.Equal(t, "TestLogger/proc: test", logger.msgs[0])
	})
}

func TestConcurrency(t *testing.T) {
	t.Run("should handle concurrent writes", func(t *testing.T) {
		logger := new(mockLogger)
		writer := New(logger, "proc").(*stdwriter)
		wg := sync.WaitGroup{}
		wg.Add(2)

		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				fmt.Fprintf(writer, "test %d\n", i)
			}
		}()

		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				fmt.Fprintf(writer, "test %d\n", i)
			}
		}()

		wg.Wait()

		assert.Equal(t, 0, writer.buf.Len())
		assert.Len(t, logger.msgs, 2000)

		for _, msg := range logger.msgs {
			assert.Contains(t, msg, "TestLogger/proc: test ")
		}
	})
}
