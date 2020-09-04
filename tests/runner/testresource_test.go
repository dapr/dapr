// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runner

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockDisposable is the mock of Disposable interface
type MockDisposable struct {
	mock.Mock
}

func (m *MockDisposable) Name() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockDisposable) Init() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockDisposable) Dispose(wait bool) error {
	args := m.Called()
	return args.Error(0)
}

func TestAdd(t *testing.T) {
	resource := new(TestResources)

	for i := 0; i < 3; i++ {
		r := new(MockDisposable)
		r.On("Name").Return(fmt.Sprintf("resource - %d", i))
		resource.Add(r)
	}

	for i, r := range resource.resources {
		assert.Equal(t, fmt.Sprintf("resource - %d", i), r.Name())
	}
}

func TestSetup(t *testing.T) {
	t.Run("active all resources", func(t *testing.T) {
		resource := new(TestResources)
		for i := 0; i < 3; i++ {
			r := new(MockDisposable)
			r.On("Name").Return(fmt.Sprintf("resource - %d", i))
			r.On("Init").Return(nil)
			resource.Add(r)
		}

		err := resource.setup()
		assert.NoError(t, err)

		for i := 2; i >= 0; i-- {
			r := resource.popActiveResource()
			assert.Equal(t, fmt.Sprintf("resource - %d", i), r.Name())
		}
	})

	t.Run("fails to setup resources and stops the process", func(t *testing.T) {
		resource := new(TestResources)
		for i := 0; i < 3; i++ {
			r := new(MockDisposable)
			r.On("Name").Return(fmt.Sprintf("resource - %d", i))
			if i != 1 {
				r.On("Init").Return(nil)
			} else {
				r.On("Init").Return(fmt.Errorf("setup error"))
			}
			resource.Add(r)
		}

		err := resource.setup()
		assert.Error(t, err)

		for i := 1; i >= 0; i-- {
			r := resource.popActiveResource()
			assert.Equal(t, fmt.Sprintf("resource - %d", i), r.Name())
		}

		r := resource.popActiveResource()
		assert.Nil(t, r)
	})
}

func TestTearDown(t *testing.T) {
	t.Run("tear down successfully", func(t *testing.T) {
		// adding 3 mock resources
		resource := new(TestResources)
		for i := 0; i < 3; i++ {
			r := new(MockDisposable)
			r.On("Name").Return(fmt.Sprintf("resource - %d", i))
			r.On("Init").Return(nil)
			r.On("Dispose").Return(nil)
			resource.Add(r)
		}

		// setup resources
		err := resource.setup()
		assert.NoError(t, err)

		// tear down all resources
		err = resource.tearDown()
		assert.NoError(t, err)

		r := resource.popActiveResource()
		assert.Nil(t, r)
	})

	t.Run("ignore failures of disposing resources", func(t *testing.T) {
		// adding 3 mock resources
		resource := new(TestResources)
		for i := 0; i < 3; i++ {
			r := new(MockDisposable)
			r.On("Name").Return(fmt.Sprintf("resource - %d", i))
			r.On("Init").Return(nil)
			if i == 1 {
				r.On("Dispose").Return(fmt.Errorf("dispose error"))
			} else {
				r.On("Dispose").Return(nil)
			}
			resource.Add(r)
		}

		// setup resources
		err := resource.setup()
		assert.NoError(t, err)

		// tear down all resources
		err = resource.tearDown()
		assert.Error(t, err)

		r := resource.popActiveResource()
		assert.Nil(t, r)
	})
}
