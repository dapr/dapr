/*
Copyright 2021 The Dapr Authors
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

package v1

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReplayableRequest(t *testing.T) {
	// Test with a 100KB message
	message := make([]byte, 100<<10)
	_, err := io.ReadFull(rand.Reader, message)
	require.NoError(t, err)

	newReplayable := func() *replayableRequest {
		rr := &replayableRequest{}
		rr.WithRawData(bytes.NewReader(message))
		rr.SetReplay(true)
		return rr
	}

	t.Run("read once", func(t *testing.T) {
		rr := newReplayable()
		defer rr.Close()

		t.Run("first read in full", func(t *testing.T) {
			read, err := io.ReadAll(rr.RawData())
			assert.NoError(t, err)
			assert.Equal(t, message, read)
		})

		t.Run("rr.data is EOF", func(t *testing.T) {
			buf := make([]byte, 9)
			n, err := io.ReadFull(rr.data, buf)
			assert.Equal(t, 0, n)
			assert.ErrorIs(t, err, io.EOF)
		})

		t.Run("replay buffer is full", func(t *testing.T) {
			assert.Equal(t, len(message), rr.replay.Len())
			read, err := io.ReadAll(bytes.NewReader(rr.replay.Bytes()))
			assert.NoError(t, err)
			assert.Equal(t, message, read)
		})

		t.Run("close rr", func(t *testing.T) {
			err := rr.Close()
			assert.NoError(t, err)
			assert.Nil(t, rr.data)
			assert.Nil(t, rr.replay)
		})
	})

	t.Run("read in full three times", func(t *testing.T) {
		rr := newReplayable()
		defer rr.Close()

		t.Run("first read in full", func(t *testing.T) {
			read, err := io.ReadAll(rr.RawData())
			assert.NoError(t, err)
			assert.Equal(t, message, read)
		})

		t.Run("rr.data is EOF", func(t *testing.T) {
			buf := make([]byte, 9)
			n, err := io.ReadFull(rr.data, buf)
			assert.Equal(t, 0, n)
			assert.ErrorIs(t, err, io.EOF)
		})

		t.Run("replay buffer is full", func(t *testing.T) {
			assert.Equal(t, len(message), rr.replay.Len())
			read, err := io.ReadAll(bytes.NewReader(rr.replay.Bytes()))
			assert.NoError(t, err)
			assert.Equal(t, message, read)
		})

		t.Run("second read in full", func(t *testing.T) {
			read, err := io.ReadAll(rr.RawData())
			assert.NoError(t, err)
			assert.Equal(t, message, read)
		})

		t.Run("third read in full", func(t *testing.T) {
			read, err := io.ReadAll(rr.RawData())
			assert.NoError(t, err)
			assert.Equal(t, message, read)
		})

		t.Run("close rr", func(t *testing.T) {
			err := rr.Close()
			assert.NoError(t, err)
			assert.Nil(t, rr.data)
			assert.Nil(t, rr.replay)
		})
	})

	t.Run("read in full, then partial read", func(t *testing.T) {
		rr := newReplayable()
		defer rr.Close()

		t.Run("first read in full", func(t *testing.T) {
			read, err := io.ReadAll(rr.RawData())
			assert.NoError(t, err)
			assert.Equal(t, message, read)
		})

		// Read minByteSliceCapacity + 100
		// This is more than the default size of the replay buffer
		partial := minByteSliceCapacity + 100

		r := rr.RawData()
		t.Run("second, partial read", func(t *testing.T) {
			buf := make([]byte, partial)
			n, err := io.ReadFull(r, buf)
			assert.NoError(t, err)
			assert.Equal(t, partial, n)
			assert.Equal(t, message[:partial], buf)
		})

		t.Run("read rest", func(t *testing.T) {
			read, err := io.ReadAll(r)
			assert.NoError(t, err)
			assert.Len(t, read, len(message)-partial)
			// Continue from byte "partial"
			assert.Equal(t, message[partial:], read)
		})

		t.Run("second read in full", func(t *testing.T) {
			read, err := io.ReadAll(rr.RawData())
			assert.NoError(t, err)
			assert.Equal(t, message, read)
		})

		t.Run("close rr", func(t *testing.T) {
			err := rr.Close()
			assert.NoError(t, err)
			assert.Nil(t, rr.data)
			assert.Nil(t, rr.replay)
		})
	})

	t.Run("partial read, then read in full", func(t *testing.T) {
		rr := newReplayable()
		defer rr.Close()

		// Read minByteSliceCapacity + 100
		// This is more than the default size of the replay buffer
		partial := minByteSliceCapacity + 100

		t.Run("first, partial read", func(t *testing.T) {
			buf := make([]byte, partial)
			n, err := io.ReadFull(rr.RawData(), buf)

			assert.NoError(t, err)
			assert.Equal(t, partial, n)
			assert.Equal(t, message[:partial], buf)
		})

		t.Run("replay buffer has partial data", func(t *testing.T) {
			assert.Equal(t, partial, rr.replay.Len())
			read, err := io.ReadAll(bytes.NewReader(rr.replay.Bytes()))
			assert.NoError(t, err)
			assert.Equal(t, message[:partial], read)
		})

		t.Run("second read in full", func(t *testing.T) {
			read, err := io.ReadAll(rr.RawData())
			assert.NoError(t, err)
			assert.Equal(t, message, read)
		})

		t.Run("rr.data is EOF", func(t *testing.T) {
			buf := make([]byte, partial)
			n, err := io.ReadFull(rr.data, buf)
			assert.Equal(t, 0, n)
			assert.ErrorIs(t, err, io.EOF)
		})

		t.Run("replay buffer is full", func(t *testing.T) {
			assert.Equal(t, len(message), rr.replay.Len())
			read, err := io.ReadAll(bytes.NewReader(rr.replay.Bytes()))
			assert.NoError(t, err)
			assert.Equal(t, message, read)
		})

		t.Run("third read in full", func(t *testing.T) {
			read, err := io.ReadAll(rr.RawData())
			assert.NoError(t, err)
			assert.Equal(t, message, read)
		})

		t.Run("close rr", func(t *testing.T) {
			err := rr.Close()
			assert.NoError(t, err)
			assert.Nil(t, rr.data)
			assert.Nil(t, rr.replay)
		})
	})
}
