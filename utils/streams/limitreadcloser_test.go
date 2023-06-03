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

package streams

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLimitReadCloser(t *testing.T) {
	t.Run("stream shorter than limit", func(t *testing.T) {
		s := LimitReadCloser(io.NopCloser(strings.NewReader("e ho guardato dentro un'emozione")), 1000)
		read, err := io.ReadAll(s)
		require.NoError(t, err)
		require.Equal(t, "e ho guardato dentro un'emozione", string(read))

		// Reading again should return io.EOF
		n, err := s.Read(read)
		require.ErrorIs(t, err, io.EOF)
		require.Equal(t, 0, n)
	})

	t.Run("stream has same length as limit", func(t *testing.T) {
		s := LimitReadCloser(io.NopCloser(strings.NewReader("e ci ho visto dentro tanto amore")), 32)
		read, err := io.ReadAll(s)
		require.NoError(t, err)
		require.Equal(t, "e ci ho visto dentro tanto amore", string(read))

		// Reading again should return io.EOF
		n, err := s.Read(read)
		require.ErrorIs(t, err, io.EOF)
		require.Equal(t, 0, n)
	})

	t.Run("stream longer than limit", func(t *testing.T) {
		s := LimitReadCloser(io.NopCloser(strings.NewReader("che ho capito perche' non si comanda al cuore")), 21)
		read, err := io.ReadAll(s)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrStreamTooLarge)
		require.Equal(t, "che ho capito perche'", string(read))

		// Reading again should return ErrStreamTooLarge again
		n, err := s.Read(read)
		require.ErrorIs(t, err, ErrStreamTooLarge)
		require.Equal(t, 0, n)
	})

	t.Run("stream longer than limit, read with byte slice", func(t *testing.T) {
		s := LimitReadCloser(io.NopCloser(strings.NewReader("e va bene cosi'")), 4)

		read := make([]byte, 100)
		n, err := s.Read(read)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrStreamTooLarge)
		require.Equal(t, "e va", string(read[0:n]))

		// Reading again should return ErrStreamTooLarge again
		n, err = s.Read(read)
		require.ErrorIs(t, err, ErrStreamTooLarge)
		require.Equal(t, 0, n)
	})

	t.Run("read in two segments", func(t *testing.T) {
		s := LimitReadCloser(io.NopCloser(strings.NewReader("senza parole")), 9)

		read := make([]byte, 5)

		n, err := s.Read(read)
		require.NoError(t, err)
		require.Equal(t, "senza", string(read[0:n]))

		n, err = s.Read(read)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrStreamTooLarge)
		require.Equal(t, " par", string(read[0:n]))

		// Reading again should return ErrStreamTooLarge again
		n, err = s.Read(read)
		require.ErrorIs(t, err, ErrStreamTooLarge)
		require.Equal(t, 0, n)
	})

	t.Run("close early", func(t *testing.T) {
		s := LimitReadCloser(io.NopCloser(strings.NewReader("senza parole")), 10)

		// Read 5 bytes then close
		read := make([]byte, 5)
		n, err := s.Read(read)
		require.NoError(t, err)
		require.Equal(t, "senza", string(read[0:n]))

		// Reading should now return io.EOF
		err = s.Close()
		require.NoError(t, err)

		n, err = s.Read(read)
		require.Error(t, err)
		require.ErrorIs(t, err, io.EOF)
		require.Equal(t, 0, n)
	})

	t.Run("stream is nil", func(t *testing.T) {
		s := LimitReadCloser(nil, 10)

		// Reading should return ErrStreamTooLarge again
		n, err := s.Read(make([]byte, 10))
		require.ErrorIs(t, err, ErrStreamTooLarge)
		require.Equal(t, 0, n)
	})
}
