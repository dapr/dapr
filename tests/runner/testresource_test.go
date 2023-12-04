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

package runner

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"
)

// MockDisposable is the mock of Disposable interface.
type MockDisposable struct {
	mock.Mock
}

func (m *MockDisposable) Name() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockDisposable) Init(ctx context.Context) error {
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
		expect := []string{}
		resource := new(TestResources)
		for i := 0; i < 3; i++ {
			name := fmt.Sprintf("resource - %d", i)
			r := new(MockDisposable)
			r.On("Name").Return(name)
			r.On("Init").Return(nil)
			resource.Add(r)
			expect = append(expect, name)
		}

		err := resource.setup()
		require.NoError(t, err)

		found := []string{}
		for i := 2; i >= 0; i-- {
			r := resource.popActiveResource()
			found = append(found, r.Name())
		}

		slices.Sort(expect)
		slices.Sort(found)
		assert.Equal(t, expect, found)
	})

	t.Run("fails to setup resources and stops the process", func(t *testing.T) {
		expect := []string{}
		resource := new(TestResources)
		for i := 0; i < 3; i++ {
			name := fmt.Sprintf("resource - %d", i)
			r := new(MockDisposable)
			r.On("Name").Return(name)
			if i != 1 {
				r.On("Init").Return(nil)
			} else {
				r.On("Init").Return(fmt.Errorf("setup error %d", i))
			}
			expect = append(expect, name)
			resource.Add(r)
		}

		err := resource.setup()
		require.Error(t, err)

		found := []string{}
		for i := 2; i >= 0; i-- {
			r := resource.popActiveResource()
			found = append(found, r.Name())
		}

		r := resource.popActiveResource()
		assert.Nil(t, r)

		slices.Sort(expect)
		slices.Sort(found)
		assert.Equal(t, expect, found)
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
		require.NoError(t, err)

		// tear down all resources
		err = resource.tearDown()
		require.NoError(t, err)

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
		require.NoError(t, err)

		// tear down all resources
		err = resource.tearDown()
		require.Error(t, err)

		r := resource.popActiveResource()
		assert.Nil(t, r)
	})
}
