// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package exporters

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoad(t *testing.T) {
	testRegistry := NewRegistry()
	Load()

	t.Run("zipkin is registered", func(t *testing.T) {
		p, e := testRegistry.CreateExporter("exporters.zipkin")
		assert.NotNil(t, p)
		assert.Nil(t, e)
	})

	t.Run("string is registered", func(t *testing.T) {
		p, e := testRegistry.CreateExporter("exporters.string")
		assert.NotNil(t, p)
		assert.Nil(t, e)
	})

}
