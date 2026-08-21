/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
*/

package binarystore_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contrib "github.com/dapr/components-contrib/binarystore"
	"github.com/dapr/dapr/pkg/components/binarystore"
	"github.com/dapr/kit/logger"
)

type testStore struct{ contrib.BinaryStore }

func TestRegistry(t *testing.T) {
	registry := binarystore.NewRegistry()
	store := &testStore{}
	registry.RegisterComponent(func(_ logger.Logger) contrib.BinaryStore { return store }, "Memory", "memory/v2")

	t.Run("initial version resolves unversioned registration", func(t *testing.T) {
		got, err := registry.Create("BINARystore.memory", "v1", "test")
		require.NoError(t, err)
		assert.Same(t, store, got)
	})

	t.Run("explicit version resolves case insensitively", func(t *testing.T) {
		got, err := registry.Create("binarystore.memory", "V2", "test")
		require.NoError(t, err)
		assert.Same(t, store, got)
	})

	t.Run("unknown component returns an error", func(t *testing.T) {
		got, err := registry.Create("binarystore.missing", "v1", "")
		assert.Nil(t, got)
		require.Error(t, err)
		assert.True(t, strings.Contains(err.Error(), "couldn't find binary store"))
	})
}
