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

//nolint:nosnakecase
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
