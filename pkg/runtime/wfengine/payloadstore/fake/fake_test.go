/*
Copyright 2026 The Dapr Authors
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

package fake

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/runtime/wfengine/payloadstore"
)

func TestShouldOffload(t *testing.T) {
	t.Parallel()

	t.Run("defaults to DefaultThreshold", func(t *testing.T) {
		t.Parallel()
		f := New()
		assert.False(t, f.ShouldOffload(payloadstore.DefaultThreshold-1))
		assert.True(t, f.ShouldOffload(payloadstore.DefaultThreshold))
	})

	t.Run("configured threshold, at-or-above semantics", func(t *testing.T) {
		t.Parallel()
		f := New().WithThreshold(10)
		assert.False(t, f.ShouldOffload(9))
		assert.True(t, f.ShouldOffload(10))
		assert.True(t, f.ShouldOffload(11))
	})
}

func TestPutGetRoundTrip(t *testing.T) {
	t.Parallel()

	f := New()
	data := []byte("a large workflow payload")

	ref, err := f.Put(t.Context(), "instance-1", data)
	require.NoError(t, err)
	assert.Equal(t, sha256.Sum256(data), ref.Checksum)
	assert.Equal(t, uint64(len(data)), ref.Size)
	assert.NotEmpty(t, ref.Key)

	got, err := f.Get(t.Context(), ref)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestPutIsIdempotentForIdenticalData(t *testing.T) {
	t.Parallel()

	f := New()
	data := []byte("same bytes")

	ref1, err := f.Put(t.Context(), "instance-1", data)
	require.NoError(t, err)
	ref2, err := f.Put(t.Context(), "instance-1", data)
	require.NoError(t, err)

	assert.Equal(t, ref1, ref2)
	assert.Equal(t, 2, f.PutCalls())
}

func TestGetUnknownReference(t *testing.T) {
	t.Parallel()

	f := New()
	_, err := f.Get(t.Context(), payloadstore.Reference{Key: "no-such-key"})
	require.Error(t, err)
}

func TestGetReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()

	f := New()
	ref, err := f.Put(t.Context(), "instance-1", []byte("original data"))
	require.NoError(t, err)

	got, err := f.Get(t.Context(), ref)
	require.NoError(t, err)
	for i := range got {
		got[i] = 'X'
	}

	// Mutating the returned slice must not corrupt the stored payload.
	again, err := f.Get(t.Context(), ref)
	require.NoError(t, err)
	assert.Equal(t, "original data", string(again))
}

func TestGetDetectsTamperedData(t *testing.T) {
	t.Parallel()

	f := New()
	ref, err := f.Put(t.Context(), "instance-1", []byte("original data"))
	require.NoError(t, err)

	f.Tamper(ref.Key, []byte("tampered data"))

	_, err = f.Get(t.Context(), ref)
	require.ErrorContains(t, err, "checksum")
}

func TestErrorInjection(t *testing.T) {
	t.Parallel()

	errPut := errors.New("put exploded")
	errGet := errors.New("get exploded")

	f := New().
		WithPutFn(func(context.Context, string, []byte) (payloadstore.Reference, error) {
			return payloadstore.Reference{}, errPut
		}).
		WithGetFn(func(context.Context, payloadstore.Reference) ([]byte, error) {
			return nil, errGet
		})

	_, err := f.Put(t.Context(), "instance-1", []byte("x"))
	require.ErrorIs(t, err, errPut)

	_, err = f.Get(t.Context(), payloadstore.Reference{})
	require.ErrorIs(t, err, errGet)
}
