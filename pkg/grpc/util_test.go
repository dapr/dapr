// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"testing"

	"github.com/stretchr/testify/assert"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
)

func TestConsistency(t *testing.T) {
	t.Run("valid eventual", func(t *testing.T) {
		c := stateConsistencyToString(commonv1pb.StateOptions_CONSISTENCY_EVENTUAL)
		assert.Equal(t, "eventual", c)
	})

	t.Run("valid strong", func(t *testing.T) {
		c := stateConsistencyToString(commonv1pb.StateOptions_CONSISTENCY_STRONG)
		assert.Equal(t, "strong", c)
	})

	t.Run("empty when invalid", func(t *testing.T) {
		c := stateConsistencyToString(commonv1pb.StateOptions_CONSISTENCY_UNSPECIFIED)
		assert.Empty(t, c)
	})
}

func TestConcurrency(t *testing.T) {
	t.Run("valid first write", func(t *testing.T) {
		c := stateConcurrencyToString(commonv1pb.StateOptions_CONCURRENCY_FIRST_WRITE)
		assert.Equal(t, "first-write", c)
	})

	t.Run("valid last write", func(t *testing.T) {
		c := stateConcurrencyToString(commonv1pb.StateOptions_CONCURRENCY_LAST_WRITE)
		assert.Equal(t, "last-write", c)
	})

	t.Run("empty when invalid", func(t *testing.T) {
		c := stateConcurrencyToString(commonv1pb.StateOptions_CONCURRENCY_UNSPECIFIED)
		assert.Empty(t, c)
	})
}
