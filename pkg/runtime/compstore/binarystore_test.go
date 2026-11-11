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

package compstore_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contrib "github.com/dapr/components-contrib/binarystore"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

type testStore struct{ contrib.BinaryStore }

func TestBinaryStoreLifecycle(t *testing.T) {
	store := compstore.New()
	first := &testStore{}
	second := &testStore{}

	store.AddBinaryStore("files", first)

	got, ok := store.GetBinaryStore("files")
	require.True(t, ok)
	assert.Same(t, first, got)

	store.AddBinaryStore("files", second)
	got, ok = store.GetBinaryStore("files")
	require.True(t, ok)
	assert.Same(t, second, got)

	store.DeleteBinaryStore("files")
	_, ok = store.GetBinaryStore("files")
	assert.False(t, ok)
}
