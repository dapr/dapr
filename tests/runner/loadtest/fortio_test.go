/*
Copyright 2022 The Dapr Authors
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

package loadtest

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/perf"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

// mockPlatform is the mock of Disposable interface.
type mockPlatform struct {
	mock.Mock
	runner.PlatformInterface
}

func (m *mockPlatform) AddApps(apps []kube.AppDescription) error {
	args := m.Called(apps)
	return args.Error(0)
}

func (m *mockPlatform) AcquireAppExternalURL(name string) string {
	args := m.Called(name)
	return args.String(0)
}

func TestFortio(t *testing.T) {
	t.Run("WithAffinity should set test affinity", func(t *testing.T) {
		const affinity = "test-affinity"
		f := NewFortio(WithAffinity(affinity))
		assert.Equal(t, affinity, f.affinityLabel)
	})
	t.Run("WithTesterImage should set test image", func(t *testing.T) {
		const testerImage = "image"
		f := NewFortio(WithTesterImage(testerImage))
		assert.Equal(t, testerImage, f.testerImage)
	})
	t.Run("WithParams should set test params", func(t *testing.T) {
		params := perf.TestParameters{}
		f := NewFortio(WithParams(params))
		assert.Equal(t, f.params, params)
	})
	t.Run("WithNumHealthChecks set the number of necessary healthchecks to consider app healthy", func(t *testing.T) {
		const numHealthCheck = 2
		f := NewFortio(WithNumHealthChecks(numHealthCheck))
		assert.Equal(t, numHealthCheck, f.numHealthChecks)
	})
	t.Run("WithTestAppName should set test app name", func(t *testing.T) {
		const appTestName = "test-app"
		f := NewFortio(WithTestAppName(appTestName))
		assert.Equal(t, appTestName, f.testApp)
	})
	t.Run("SetParams should set test params and result to nil", func(t *testing.T) {
		params := perf.TestParameters{}
		f := NewFortio(WithParams(params))
		assert.Equal(t, f.params, params)
		paramsOthers := perf.TestParameters{
			QPS: 1,
		}
		f.SetParams(paramsOthers)
		assert.Equal(t, f.params, paramsOthers)
	})
	t.Run("valiate should return error when apptesterurl is empty", func(t *testing.T) {
		require.Error(t, NewFortio().validate())
	})
	t.Run("setup should return error if AddApps return an error", func(t *testing.T) {
		errFake := errors.New("my-err")
		mockPlatform := new(mockPlatform)
		mockPlatform.On("AddApps", mock.Anything).Return(errFake)
		f := NewFortio()
		assert.Equal(t, f.setup(mockPlatform), errFake)
	})
	t.Run("setup should return error if validate returns an error", func(t *testing.T) {
		const appName = "app-test"
		mockPlatform := new(mockPlatform)
		mockPlatform.On("AddApps", mock.Anything).Return(nil)
		mockPlatform.On("AcquireAppExternalURL", appName).Return("")
		f := NewFortio(WithTestAppName(appName))
		setupErr := f.setup(mockPlatform)
		require.Error(t, setupErr)
	})
	t.Run("Run should return error when validate return an error", func(t *testing.T) {
		const appName = "app-test"
		mockPlatform := new(mockPlatform)
		mockPlatform.On("AddApps", mock.Anything).Return(nil)
		mockPlatform.On("AcquireAppExternalURL", appName).Return("")
		f := NewFortio(WithTestAppName(appName))
		setupErr := f.Run(mockPlatform)
		require.Error(t, setupErr)
		mockPlatform.AssertNumberOfCalls(t, "AcquireAppExternalURL", 1)
		require.Error(t, f.Run(mockPlatform))
		mockPlatform.AssertNumberOfCalls(t, "AcquireAppExternalURL", 1)
	})
}
