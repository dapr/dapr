/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
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

	assert.Equal(t, 0, store.BinaryStoresLen())
	store.AddBinaryStore("files", first)
	assert.Equal(t, 1, store.BinaryStoresLen())

	got, ok := store.GetBinaryStore("files")
	require.True(t, ok)
	assert.Same(t, first, got)

	store.AddBinaryStore("files", second)
	got, ok = store.GetBinaryStore("files")
	require.True(t, ok)
	assert.Same(t, second, got)

	listed := store.ListBinaryStores()
	assert.Same(t, second, listed["files"])
	delete(listed, "files")
	assert.Equal(t, 1, store.BinaryStoresLen(), "ListBinaryStores must return a copy")

	store.DeleteBinaryStore("files")
	assert.Equal(t, 0, store.BinaryStoresLen())
	_, ok = store.GetBinaryStore("files")
	assert.False(t, ok)
}
