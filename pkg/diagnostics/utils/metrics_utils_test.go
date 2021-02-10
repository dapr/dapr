// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opencensus.io/tag"
)

func TestWithTags(t *testing.T) {
	t.Run("one tag", func(t *testing.T) {
		appKey := tag.MustNewKey("app_id")
		mutators := WithTags(appKey, "test")
		assert.Equal(t, 1, len(mutators))
	})

	t.Run("two tags", func(t *testing.T) {
		appKey := tag.MustNewKey("app_id")
		operationKey := tag.MustNewKey("operation")
		mutators := WithTags(appKey, "test", operationKey, "op")
		assert.Equal(t, 2, len(mutators))
	})

	t.Run("three tags", func(t *testing.T) {
		appKey := tag.MustNewKey("app_id")
		operationKey := tag.MustNewKey("operation")
		methodKey := tag.MustNewKey("method")
		mutators := WithTags(appKey, "test", operationKey, "op", methodKey, "method")
		assert.Equal(t, 3, len(mutators))
	})

	t.Run("two tags with wrong value type", func(t *testing.T) {
		appKey := tag.MustNewKey("app_id")
		operationKey := tag.MustNewKey("operation")
		mutators := WithTags(appKey, "test", operationKey, 1)
		assert.Equal(t, 1, len(mutators))
	})

	t.Run("skip empty value key", func(t *testing.T) {
		appKey := tag.MustNewKey("app_id")
		operationKey := tag.MustNewKey("operation")
		methodKey := tag.MustNewKey("method")
		mutators := WithTags(appKey, "", operationKey, "op", methodKey, "method")
		assert.Equal(t, 2, len(mutators))
	})
}
