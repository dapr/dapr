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

package kubernetes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	apiv1 "k8s.io/api/core/v1"
)

func TestBuildDeploymentObject(t *testing.T) {
	testApp := AppDescription{
		AppName:        "testapp",
		DaprEnabled:    true,
		ImageName:      "helloworld",
		RegistryName:   "dariotest",
		Replicas:       1,
		IngressEnabled: true,
		MetricsEnabled: true,
	}

	t.Run("Unix socket", func(t *testing.T) {
		testApp.UnixDomainSocketPath = "/var/run"
		defer func() {
			testApp.UnixDomainSocketPath = "" // cleanup
		}()

		// act
		obj := buildDeploymentObject("testNamespace", testApp)

		// assert
		assert.NotNil(t, obj)
		assert.Equal(t, "/var/run", obj.Spec.Template.Annotations["dapr.io/unix-domain-socket-path"])
	})

	t.Run("Inject pluggable components", func(t *testing.T) {
		// act
		obj := buildDeploymentObject("testNamespace", AppDescription{
			InjectPluggableComponents: true,
		})

		// assert
		assert.NotNil(t, obj)
		assert.Equal(t, "true", obj.Spec.Template.Annotations["dapr.io/inject-pluggable-components"])
	})

	t.Run("Dapr Enabled", func(t *testing.T) {
		testApp.DaprEnabled = true

		// act
		obj := buildDeploymentObject("testNamespace", testApp)

		// assert
		assert.NotNil(t, obj)
		assert.Equal(t, "true", obj.Spec.Template.Annotations["dapr.io/enabled"])
	})

	t.Run("Dapr disabled", func(t *testing.T) {
		testApp.DaprEnabled = false

		// act
		obj := buildDeploymentObject("testNamespace", testApp)

		// assert
		assert.NotNil(t, obj)
		assert.Empty(t, obj.Spec.Template.Annotations)
	})
}

func TestBuildJobObject(t *testing.T) {
	testApp := AppDescription{
		AppName:        "testapp",
		DaprEnabled:    true,
		ImageName:      "helloworld",
		RegistryName:   "dariotest",
		Replicas:       1,
		IngressEnabled: true,
	}

	t.Run("Dapr Enabled", func(t *testing.T) {
		testApp.DaprEnabled = true

		// act
		obj := buildJobObject("testNamespace", testApp)

		// assert
		assert.NotNil(t, obj)
		assert.Equal(t, "true", obj.Spec.Template.Annotations["dapr.io/enabled"])
	})

	t.Run("Dapr disabled", func(t *testing.T) {
		testApp.DaprEnabled = false

		// act
		obj := buildJobObject("testNamespace", testApp)

		// assert
		assert.NotNil(t, obj)
		assert.Empty(t, obj.Spec.Template.Annotations)
	})
}

func TestBuildServiceObject(t *testing.T) {
	testApp := AppDescription{
		AppName:        "testapp",
		DaprEnabled:    true,
		ImageName:      "helloworld",
		RegistryName:   "dariotest",
		Replicas:       1,
		IngressEnabled: true,
		MetricsEnabled: true,
	}

	t.Run("Ingress is enabled", func(t *testing.T) {
		testApp.IngressEnabled = true

		// act
		obj := buildServiceObject("testNamespace", testApp)

		// assert
		assert.NotNil(t, obj)
		assert.Equal(t, apiv1.ServiceTypeLoadBalancer, obj.Spec.Type)
	})

	t.Run("Ingress is disabled", func(t *testing.T) {
		testApp.IngressEnabled = false

		// act
		obj := buildServiceObject("testNamespace", testApp)

		// assert
		assert.NotNil(t, obj)
		assert.Equal(t, apiv1.ServiceTypeClusterIP, obj.Spec.Type)
	})
}
