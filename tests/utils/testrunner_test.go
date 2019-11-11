package utils

import (
	"fmt"
	"testing"

	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type fakeTestingM struct{}

func (f *fakeTestingM) Run() int {
	return 0
}

// MockPlatform is the mock of Disposable interface
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

func (m *MockPlatform) AddTestApps(apps []kube.AppDescription) error {
	args := m.Called(apps)
	return args.Error(0)
}

func (m *MockPlatform) InstallApps() error {
	args := m.Called()
	return args.Error(0)
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
		},
		{
			AppName:        "fakeapp1",
			DaprEnabled:    true,
			ImageName:      "fakeapp",
			RegistryName:   "fakeregistry",
			Replicas:       1,
			IngressEnabled: true,
		},
	}

	t.Run("Run Runner successfully", func(t *testing.T) {
		mockPlatform := new(MockPlatform)
		mockPlatform.On("tearDown").Return(nil)
		mockPlatform.On("setup").Return(nil)
		mockPlatform.On("AddTestApps", fakeTestApps).Return(nil)
		mockPlatform.On("InstallApps").Return(nil)

		fakeRunner := &TestRunner{
			id:          "fakeRunner",
			initialApps: fakeTestApps,
			Platform:    mockPlatform,
		}

		ret := fakeRunner.Start(&fakeTestingM{})
		assert.Equal(t, 0, ret)

		mockPlatform.AssertNumberOfCalls(t, "setup", 1)
		mockPlatform.AssertNumberOfCalls(t, "tearDown", 1)
		mockPlatform.AssertNumberOfCalls(t, "AddTestApps", 1)
		mockPlatform.AssertNumberOfCalls(t, "InstallApps", 1)
	})

	t.Run("setup is failed, but teardown is called", func(t *testing.T) {
		mockPlatform := new(MockPlatform)
		mockPlatform.On("setup").Return(fmt.Errorf("setup is failed"))
		mockPlatform.On("tearDown").Return(nil)
		mockPlatform.On("AddTestApps", fakeTestApps).Return(nil)
		mockPlatform.On("InstallApps").Return(nil)

		fakeRunner := &TestRunner{
			id:          "fakeRunner",
			initialApps: fakeTestApps,
			Platform:    mockPlatform,
		}

		ret := fakeRunner.Start(&fakeTestingM{})
		assert.Equal(t, 1, ret)

		mockPlatform.AssertNumberOfCalls(t, "setup", 1)
		mockPlatform.AssertNumberOfCalls(t, "tearDown", 1)
		mockPlatform.AssertNumberOfCalls(t, "AddTestApps", 0)
		mockPlatform.AssertNumberOfCalls(t, "InstallApps", 0)
	})
}
