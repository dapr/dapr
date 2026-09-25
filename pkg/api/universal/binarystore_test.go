/*
Copyright 2025 The Dapr Authors
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

package universal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/binarystore"
	"github.com/dapr/components-contrib/binarystore/fake"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

func newBinaryTestUniversal(t *testing.T) (*Universal, binarystore.BinaryStore) {
	t.Helper()
	compStore := compstore.New()
	store := fake.NewFake(testLogger)
	compStore.AddBinaryStore("mystore", store)
	u := &Universal{
		logger:     testLogger,
		resiliency: resiliency.New(nil),
		compStore:  compStore,
	}
	return u, store
}

func TestBinaryStore_SetGetDeleteRoundTrip(t *testing.T) {
	u, _ := newBinaryTestUniversal(t)
	ctx := context.Background()

	require.NoError(t, u.SetBinaryFileAlpha1(ctx, "mystore", "file.bin", true, bytes.NewReader([]byte("payload"))))

	body, err := u.GetBinaryFileAlpha1(ctx, "mystore", "file.bin")
	require.NoError(t, err)
	defer body.Close()
	got, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, []byte("payload"), got)

	require.NoError(t, u.DeleteBinaryFileAlpha1(ctx, "mystore", "file.bin"))

	_, err = u.GetBinaryFileAlpha1(ctx, "mystore", "file.bin")
	require.ErrorIs(t, err, messages.ErrBinaryStoreFileNotFound)
}

func TestBinaryStore_SetNoOverwriteConflicts(t *testing.T) {
	u, _ := newBinaryTestUniversal(t)
	ctx := context.Background()

	require.NoError(t, u.SetBinaryFileAlpha1(ctx, "mystore", "f", true, bytes.NewReader([]byte("a"))))
	err := u.SetBinaryFileAlpha1(ctx, "mystore", "f", false, bytes.NewReader([]byte("b")))
	require.ErrorIs(t, err, messages.ErrBinaryStoreFileExists)
}

func TestBinaryStore_OverwriteReplaces(t *testing.T) {
	u, _ := newBinaryTestUniversal(t)
	ctx := context.Background()

	require.NoError(t, u.SetBinaryFileAlpha1(ctx, "mystore", "f", true, bytes.NewReader([]byte("a"))))
	require.NoError(t, u.SetBinaryFileAlpha1(ctx, "mystore", "f", true, bytes.NewReader([]byte("bb"))))
	body, err := u.GetBinaryFileAlpha1(ctx, "mystore", "f")
	require.NoError(t, err)
	defer body.Close()
	got, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, []byte("bb"), got)
}

func TestBinaryStore_GetMissingReturnsNotFound(t *testing.T) {
	u, _ := newBinaryTestUniversal(t)
	_, err := u.GetBinaryFileAlpha1(context.Background(), "mystore", "nope")
	require.ErrorIs(t, err, messages.ErrBinaryStoreFileNotFound)
}

func TestBinaryStore_DeleteMissingReturnsNotFound(t *testing.T) {
	u, _ := newBinaryTestUniversal(t)
	err := u.DeleteBinaryFileAlpha1(context.Background(), "mystore", "nope")
	require.ErrorIs(t, err, messages.ErrBinaryStoreFileNotFound)
}

func TestBinaryStore_MissingFileName(t *testing.T) {
	u, _ := newBinaryTestUniversal(t)
	ctx := context.Background()

	require.ErrorIs(t, u.SetBinaryFileAlpha1(ctx, "mystore", "", true, bytes.NewReader([]byte("x"))), messages.ErrBinaryStoreNameMissing)
	_, err := u.GetBinaryFileAlpha1(ctx, "mystore", "")
	require.ErrorIs(t, err, messages.ErrBinaryStoreNameMissing)
	require.ErrorIs(t, u.DeleteBinaryFileAlpha1(ctx, "mystore", ""), messages.ErrBinaryStoreNameMissing)
}

func TestBinaryStore_ComponentNotFound(t *testing.T) {
	u, _ := newBinaryTestUniversal(t)
	ctx := context.Background()

	err := u.SetBinaryFileAlpha1(ctx, "missing", "f", true, bytes.NewReader([]byte("x")))
	require.ErrorIs(t, err, messages.ErrBinaryStoreNotFound)
}

func TestBinaryStore_LargeStreamingRoundTrip(t *testing.T) {
	u, _ := newBinaryTestUniversal(t)
	ctx := context.Background()

	// 1 MiB payload exercises the streaming io.Reader path without buffering.
	size := 1024 * 1024
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	require.NoError(t, u.SetBinaryFileAlpha1(ctx, "mystore", "big.bin", true, bytes.NewReader(payload)))
	body, err := u.GetBinaryFileAlpha1(ctx, "mystore", "big.bin")
	require.NoError(t, err)
	defer body.Close()
	got, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Len(t, got, size)
	assert.True(t, bytes.Equal(payload, got))
}

// mapBinaryStoreError should wrap unknown errors with the fallback APIError
// (preserving the operation-specific tag/HTTP code) rather than passing them
// through verbatim.
func TestBinaryStore_mapBinaryStoreErrorWrapsUnknown(t *testing.T) {
	err := mapBinaryStoreError(errors.New("boom"), "c", "f", messages.ErrBinaryStoreSet)
	require.Error(t, err)
	apiErr, ok := err.(messages.APIError)
	require.True(t, ok, "expected an APIError for unknown errors")
	assert.Equal(t, messages.ErrBinaryStoreSet.Tag(), apiErr.Tag())
}
