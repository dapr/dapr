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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteCloser(t *testing.T) {
	t.Run("no writers", func(t *testing.T) {
		w := WriteCloser()
		n, err := w.Write([]byte("test"))
		require.NoError(t, err)
		assert.Equal(t, 4, n)

		require.NoError(t, w.Close())
	})

	t.Run("simple writers", func(t *testing.T) {
		pr1, pw1 := io.Pipe()
		pr2, pw2 := io.Pipe()
		pr3, pw3 := io.Pipe()

		w := WriteCloser(pw1, pw2, pw3)

		var n int
		var werr error
		done := make(chan struct{})
		go func() {
			n, werr = w.Write([]byte("test"))
			close(done)
		}()

		for _, reader := range []io.Reader{pr1, pr2, pr3} {
			resp := make([]byte, 1024)
			rn, err := reader.Read(resp)
			require.NoError(t, err)
			assert.Equal(t, 4, rn)
			assert.Equal(t, "test", string(resp[:rn]))
		}

		select {
		case <-done:
			assert.Equal(t, 4, n)
			require.NoError(t, werr)
		case <-time.After(time.Second):
			require.Fail(t, "timeout waiting for write to complete")
		}

		require.NoError(t, w.Close())
		for _, reader := range []io.Reader{pr1, pr2, pr3} {
			resp := make([]byte, 1024)
			n, err := reader.Read(resp)
			require.ErrorIs(t, err, io.EOF)
			assert.Equal(t, 0, n)
		}
	})

	t.Run("read all", func(t *testing.T) {
		pr, pw := io.Pipe()
		w := WriteCloser(pw)

		var n int
		var werr error
		done := make(chan struct{})
		go func() {
			n, werr = w.Write([]byte("test"))
			werr = errors.Join(werr, w.Close())
			close(done)
		}()

		all, err := io.ReadAll(pr)
		require.NoError(t, err)
		assert.Equal(t, "test", string(all))

		select {
		case <-done:
			assert.Equal(t, 4, n)
			require.NoError(t, werr)
		case <-time.After(time.Second):
			require.Fail(t, "timeout waiting for write to complete")
		}
	})

	t.Run("buffered writes", func(t *testing.T) {
		pr1, pw1 := io.Pipe()
		pr2, pw2 := io.Pipe()
		w := WriteCloser(pw1, pw2)

		var wn int
		var werr error
		done := make(chan struct{})
		go func() {
			wn, werr = w.Write([]byte("helloworld"))
			werr = errors.Join(werr, w.Close())
			close(done)
		}()

		var pr2resp atomic.Value
		go func() {
			resp := make([]byte, 1024)
			pr2.Read(resp)
			pr2resp.Store(string(resp))
		}()

		resp := make([]byte, 2)
		n, err := pr1.Read(resp)
		require.NoError(t, err)
		assert.Equal(t, 2, n)
		assert.Equal(t, "he", string(resp))

		select {
		case <-done:
			require.Fail(t, "expected write to block until all data is read")
		default:
		}

		assert.Nil(t, pr2resp.Load())

		resp = make([]byte, 1024)
		n, err = pr1.Read(resp)
		require.NoError(t, err)
		assert.Equal(t, 8, n)
		assert.Equal(t, "lloworld", string(resp[:n]))

		select {
		case <-done:
			assert.Equal(t, 10, wn)
			require.NoError(t, werr)
		case <-time.After(time.Second):
			require.Fail(t, "timeout waiting for write to complete")
		}

		value := pr2resp.Load()
		assert.NotNil(t, value)

		strValue, ok := value.(string)
		assert.True(t, ok)

		if ok {
			assert.Equal(t, "helloworld", strValue[:10])
		}

		require.NoError(t, w.Close())
		n, err = pr1.Read(resp)
		require.ErrorIs(t, err, io.EOF)
		assert.Equal(t, 0, n)
	})
}
