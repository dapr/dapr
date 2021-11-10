// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runner

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
)

type fakeTestingM struct{}

func (f *fakeTestingM) Run() int {
	return 0
}

// MockPlatform is the mock of Disposable interface.
type MockPlatform struct {
	mock.Mock
}

func (m *MockPlatform) setup() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockPlatform) tearDown() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockPlatform) AcquireAppExternalURL(name string) string {
	args := m.Called(name)
	return args.String(0)
}

func (m *MockPlatform) GetAppHostDetails(name string) (string, string, error) {
	args := m.Called(name)
	return args.String(0), args.String(0), args.Error(0)
}

func (m *MockPlatform) addComponents(comps []kube.ComponentDescription) error {
	args := m.Called(comps)
	return args.Error(0)
}

func (m *MockPlatform) addApps(apps []kube.AppDescription) error {
	args := m.Called(apps)
	return args.Error(0)
}

func (m *MockPlatform) Scale(name string, replicas int32) error {
	args := m.Called(replicas)
	return args.Error(0)
}

func (m *MockPlatform) SetAppEnv(name, key, value string) error {
	args := m.Called(key)
	return args.Error(0)
}

func (m *MockPlatform) Restart(name string) error {
	args := m.Called(name)
	return args.Error(0)
}

func (m *MockPlatform) PortForwardToApp(appName string, targetPort ...int) ([]int, error) {
	args := m.Called(appName)
	return []int{}, args.Error(0)
}

func (m *MockPlatform) GetAppUsage(appName string) (*AppUsage, error) {
	args := m.Called(appName)
	return &AppUsage{}, args.Error(0)
}

func (m *MockPlatform) GetSidecarUsage(appName string) (*AppUsage, error) {
	args := m.Called(appName)
	return &AppUsage{}, args.Error(0)
}

func (m *MockPlatform) GetTotalRestarts(appName string) (int, error) {
	args := m.Called(appName)
	return 0, args.Error(0)
}

func TestStartRunner(t *testing.T) {
	fakeTestApps := []kube.AppDescription{
		{
			AppName:        "fakeapp",
			DaprEnabled:    true,
			ImageName:      "fakeapp",
			RegistryName:   "fakeregistry",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
		{
			AppName:        "fakeapp1",
			DaprEnabled:    true,
			ImageName:      "fakeapp",
			RegistryName:   "fakeregistry",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
	}

	fakeComps := []kube.ComponentDescription{
		{
			Name:     "statestore",
			TypeName: "state.fake",
			MetaData: map[string]string{
				"address":  "localhost",
				"password": "fakepassword",
			},
		},
	}

	t.Run("Run Runner successfully", func(t *testing.T) {
		mockPlatform := new(MockPlatform)
		mockPlatform.On("tearDown").Return(nil)
		mockPlatform.On("setup").Return(nil)
		mockPlatform.On("addApps", fakeTestApps).Return(nil)
		mockPlatform.On("addComponents", fakeComps).Return(nil)

		fakeRunner := &TestRunner{
			id:         "fakeRunner",
			components: fakeComps,
			testApps:   fakeTestApps,
			Platform:   mockPlatform,
		}

		ret := fakeRunner.Start(&fakeTestingM{})
		assert.Equal(t, 0, ret)

		mockPlatform.AssertNumberOfCalls(t, "setup", 1)
		mockPlatform.AssertNumberOfCalls(t, "tearDown", 1)
		mockPlatform.AssertNumberOfCalls(t, "addApps", 1)
		mockPlatform.AssertNumberOfCalls(t, "addComponents", 1)
	})

	t.Run("setup is failed, but teardown is called", func(t *testing.T) {
		mockPlatform := new(MockPlatform)
		mockPlatform.On("setup").Return(fmt.Errorf("setup is failed"))
		mockPlatform.On("tearDown").Return(nil)
		mockPlatform.On("addApps", fakeTestApps).Return(nil)
		mockPlatform.On("addComponents", fakeComps).Return(nil)

		fakeRunner := &TestRunner{
			id:         "fakeRunner",
			components: fakeComps,
			testApps:   fakeTestApps,
			Platform:   mockPlatform,
		}

		ret := fakeRunner.Start(&fakeTestingM{})
		assert.Equal(t, 1, ret)

		mockPlatform.AssertNumberOfCalls(t, "setup", 1)
		mockPlatform.AssertNumberOfCalls(t, "tearDown", 1)
		mockPlatform.AssertNumberOfCalls(t, "addApps", 0)
		mockPlatform.AssertNumberOfCalls(t, "addComponents", 0)
	})
}
