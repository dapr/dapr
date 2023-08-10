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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"

	configurationv1alpha1 "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
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

func (m *MockPlatform) Setup() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockPlatform) TearDown() error {
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

func (m *MockPlatform) AddComponents(comps []kube.ComponentDescription) error {
	args := m.Called(comps)
	return args.Error(0)
}

func (m *MockPlatform) AddApps(apps []kube.AppDescription) error {
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

func (m *MockPlatform) GetConfiguration(name string) (*configurationv1alpha1.Configuration, error) {
	args := m.Called(name)
	return &configurationv1alpha1.Configuration{}, args.Error(0)
}

func (m *MockPlatform) GetService(name string) (*corev1.Service, error) {
	args := m.Called(name)
	return &corev1.Service{}, args.Error(0)
}

func (m *MockPlatform) LoadTest(loadtester LoadTester) error {
	args := m.Called(loadtester)
	return args.Error(0)
}

func (m *MockPlatform) AddSecrets(secrets []kube.SecretDescription) error {
	args := m.Called(secrets)
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
			MetaData: map[string]kube.MetadataValue{
				"address":  {Raw: "localhost"},
				"password": {Raw: "fakepassword"},
			},
		},
	}

	t.Run("Run Runner successfully", func(t *testing.T) {
		mockPlatform := new(MockPlatform)
		mockPlatform.On("TearDown").Return(nil)
		mockPlatform.On("Setup").Return(nil)
		mockPlatform.On("AddApps", fakeTestApps).Return(nil)
		mockPlatform.On("AddComponents", fakeComps).Return(nil)

		fakeRunner := &TestRunner{
			id:         "fakeRunner",
			components: fakeComps,
			testApps:   fakeTestApps,
			Platform:   mockPlatform,
		}

		ret := fakeRunner.Start(&fakeTestingM{})
		assert.Equal(t, 0, ret)

		mockPlatform.AssertNumberOfCalls(t, "Setup", 1)
		mockPlatform.AssertNumberOfCalls(t, "TearDown", 1)
		mockPlatform.AssertNumberOfCalls(t, "AddApps", 1)
		mockPlatform.AssertNumberOfCalls(t, "AddComponents", 1)
	})

	t.Run("setup is failed, but teardown is called", func(t *testing.T) {
		mockPlatform := new(MockPlatform)
		mockPlatform.On("Setup").Return(fmt.Errorf("setup is failed"))
		mockPlatform.On("TearDown").Return(nil)
		mockPlatform.On("AddApps", fakeTestApps).Return(nil)
		mockPlatform.On("AddComponents", fakeComps).Return(nil)

		fakeRunner := &TestRunner{
			id:         "fakeRunner",
			components: fakeComps,
			testApps:   fakeTestApps,
			Platform:   mockPlatform,
		}

		ret := fakeRunner.Start(&fakeTestingM{})
		assert.Equal(t, 1, ret)

		mockPlatform.AssertNumberOfCalls(t, "Setup", 1)
		mockPlatform.AssertNumberOfCalls(t, "TearDown", 1)
		mockPlatform.AssertNumberOfCalls(t, "AddApps", 0)
		mockPlatform.AssertNumberOfCalls(t, "AddComponents", 0)
	})
}
