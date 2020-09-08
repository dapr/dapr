// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package exporters

import (
	"testing"

	"github.com/dapr/components-contrib/exporters"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

func TestCreateFullName(t *testing.T) {
	t.Run("create zipkin exporter key name", func(t *testing.T) {
		assert.Equal(t, "exporters.zipkin", createFullName("zipkin"))
	})

	t.Run("create string key name", func(t *testing.T) {
		assert.Equal(t, "exporters.string", createFullName("string"))
	})
}

func TestNewExporterRegistry(t *testing.T) {
	registry0 := NewRegistry()
	registry1 := NewRegistry()

	assert.Equal(t, registry0, registry1, "should be the same object")
}

func TestCreateExporter(t *testing.T) {
	testRegistry := NewRegistry()

	t.Run("exporter is registered", func(t *testing.T) {
		const ExporterName = "mockExporter"
		// Initiate mock object
		mockExporter := new(daprt.MockExporter)

		// act
		testRegistry.Register(New(ExporterName, func() exporters.Exporter {
			return mockExporter
		}))
		p, e := testRegistry.Create(createFullName(ExporterName))

		// assert
		assert.Equal(t, mockExporter, p)
		assert.Nil(t, e)
	})

	t.Run("exporter is not registered", func(t *testing.T) {
		const ExporterName = "fakeExporter"

		// act
		p, actualError := testRegistry.Create(createFullName(ExporterName))
		expectedError := errors.Errorf("couldn't find exporter %s", createFullName(ExporterName))
		// assert
		assert.Nil(t, p)
		assert.Equal(t, expectedError.Error(), actualError.Error())
	})
}
