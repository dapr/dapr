// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package custom

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	grpc_go "google.golang.org/grpc"

	"github.com/dapr/components-contrib/custom"
)

// custom mock object
type mockcustom struct {
	mock.Mock
}

// Init is a mock initialization method.
func (m *mockcustom) Init(metadata custom.Metadata) error {
	args := m.Called(metadata)
	return args.Error(0)
}

// RegisterServer add service to gRPC server.
func (m *mockcustom) RegisterServer(s *grpc_go.Server) error {
	args := m.Called(s)
	return args.Error(0)
}
func TestCreateFullName(t *testing.T) {
	t.Run("create custom exporter key name", func(t *testing.T) {
		assert.Equal(t, "custom.encrypt", createFullName("encrypt"))
	})

	t.Run("create string key name", func(t *testing.T) {
		assert.Equal(t, "custom.decrypt", createFullName("decrypt"))
	})
}

func TestCreateRegistry(t *testing.T) {
	testRegistry := NewRegistry()

	t.Run("custom component is registered", func(t *testing.T) {
		const CustomName = "mockCustom"
		// Initiate mock object
		mockCustom := new(mockcustom)

		// act
		testRegistry.Register(New(CustomName, func() custom.Custom {
			return mockCustom
		}))
		p, e := testRegistry.CreateCustomComponent(createFullName(CustomName))

		// assert
		assert.Equal(t, mockCustom, p)
		assert.Nil(t, e)
	})

	t.Run("custom component is not registered", func(t *testing.T) {
		const CustomName = "fakeCustomComponent"

		// act
		p, e := testRegistry.CreateCustomComponent(createFullName(CustomName))

		// assert
		assert.Nil(t, p)
		assert.Equal(t, fmt.Errorf("couldn't find custom component %s", createFullName(CustomName)), e)
	})
}
