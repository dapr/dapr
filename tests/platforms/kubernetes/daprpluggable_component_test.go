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

package kubernetes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	apiv1 "k8s.io/api/core/v1"
)

func TestBuildPluggableComponents(t *testing.T) {
	newTestApp := func() AppDescription {
		return AppDescription{
			AppName:        "testapp",
			DaprEnabled:    true,
			ImageName:      "helloworld",
			RegistryName:   "dariotest",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
			PluggableComponents: []apiv1.Container{
				{},
			},
		}
	}

	t.Run("adaptAndBuild should add shared socket volume", func(t *testing.T) {
		app := newTestApp()
		adaptAndBuildPluggableComponents(&app)
		assert.NotEmpty(t, app.Volumes)
	})

	t.Run("adaptAndBuild should set domain socket path when empty", func(t *testing.T) {
		app := newTestApp()
		adaptAndBuildPluggableComponents(&app)
		assert.NotEmpty(t, app.UnixDomainSocketPath)
		assert.Empty(t, app.DaprEnv)
	})

	t.Run("adaptAndBuild should set dapr env vars when domain socket path is not empty", func(t *testing.T) {
		app := newTestApp()
		app.UnixDomainSocketPath = "fake-path"
		adaptAndBuildPluggableComponents(&app)
		assert.NotEmpty(t, app.UnixDomainSocketPath)
		assert.Equal(t, app.DaprEnv, "DAPR_COMPONENTS_SOCKETS_FOLDER=fake-path")
	})

	t.Run("adaptAndBuild should concat dapr env vars when domain socket path is not empty", func(t *testing.T) {
		app := newTestApp()
		app.UnixDomainSocketPath = "fake-path"
		app.DaprEnv = "env=test"
		adaptAndBuildPluggableComponents(&app)
		assert.NotEmpty(t, app.UnixDomainSocketPath)
		assert.NotEmpty(t, app.DaprEnv)
		assert.Equal(t, app.DaprEnv, "env=test,DAPR_COMPONENTS_SOCKETS_FOLDER=fake-path")
	})

	t.Run("adaptAndBuild should return pluggable component containers with the volume mount and envvar", func(t *testing.T) {
		app := newTestApp()
		app.UnixDomainSocketPath = "fake-path"
		app.DaprEnv = "env=test"
		containers := adaptAndBuildPluggableComponents(&app)
		assert.Len(t, containers, 1)
		container := containers[0]
		assert.Len(t, container.VolumeMounts, 1)
		assert.Len(t, container.Env, 1)
	})
}
