/*
Copyright 2025 The Dapr Authors
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

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/helm"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(env))
}

type env struct {
	base                  *helm.Helm
	withOtelEnvVars       *helm.Helm
	withEscapedCommas     *helm.Helm
	withSchedulerEnabled  *helm.Helm
	withSchedulerDisabled *helm.Helm
}

func (e *env) Setup(t *testing.T) []framework.Option {
	// Base config with no custom env vars
	e.base = helm.New(t,
		helm.WithShowOnly("charts/dapr_sidecar_injector", "dapr_sidecar_injector_deployment.yaml"),
	)

	e.withSchedulerEnabled = helm.New(t,
		helm.WithShowOnly("charts/dapr_sidecar_injector", "dapr_sidecar_injector_deployment.yaml"),
		helm.WithValues(
			"global.scheduler.enabled=true",
		),
	)
	e.withSchedulerDisabled = helm.New(t,
		helm.WithShowOnly("charts/dapr_sidecar_injector", "dapr_sidecar_injector_deployment.yaml"),
		helm.WithValues(
			"global.scheduler.enabled=false",
		),
	)

	// config with OTEL env vars
	e.withOtelEnvVars = helm.New(t,
		helm.WithShowOnly("charts/dapr_sidecar_injector", "dapr_sidecar_injector_deployment.yaml"),
		helm.WithValues(
			"dapr_sidecar_injector.extraEnvVars.OTEL_SERVICE_NAME=my-dapr-sidecar",
			"dapr_sidecar_injector.extraEnvVars.OTEL_RESOURCE_ATTRIBUTES=k8s.pod.name=my-pod",
			"dapr_sidecar_injector.extraEnvVars.OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector-opentelemetry-collector.opentelemetry.svc.cluster.local:4317",
		),
	)

	// config with escaped commas to test complex OTEL_RESOURCE_ATTRIBUTES
	e.withEscapedCommas = helm.New(t,
		helm.WithShowOnly("charts/dapr_sidecar_injector", "dapr_sidecar_injector_deployment.yaml"),
		helm.WithValues(
			"dapr_sidecar_injector.extraEnvVars.OTEL_SERVICE_NAME=escaped-test",
			"dapr_sidecar_injector.extraEnvVars.OTEL_RESOURCE_ATTRIBUTES=k8s.pod.name=my-pod\\,k8s.namespace.name=default\\,k8s.deployment.name=my-app",
			"dapr_sidecar_injector.extraEnvVars.OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317",
		),
	)

	return []framework.Option{
		framework.WithProcesses(
			e.base,
			e.withOtelEnvVars,
			e.withEscapedCommas,
			e.withSchedulerEnabled,
			e.withSchedulerDisabled,
		),
	}
}

func (e *env) Run(t *testing.T, ctx context.Context) {
	findInjectorDeployment := func(deployments []appsv1.Deployment) *appsv1.Deployment {
		for i := range deployments {
			if deployments[i].Name == "dapr-sidecar-injector" {
				return &deployments[i]
			}
		}
		return nil
	}

	t.Run("base config", func(t *testing.T) {
		deployments := helm.UnmarshalStdout[appsv1.Deployment](t, e.base)
		injectorDeployment := findInjectorDeployment(deployments)
		require.NotNil(t, injectorDeployment, "dapr-sidecar-injector deployment should exist")
		containers := injectorDeployment.Spec.Template.Spec.Containers
		assert.NotEmpty(t, containers, "Sidecar injector should have at least one container")

		// No OTEL env vars are set by default
		injectorContainer := containers[0]
		for _, envvar := range injectorContainer.Env {
			assert.NotContains(t, envvar.Name, "OTEL", "Base configuration should not have any OTEL environment variables")
		}
	})

	t.Run("OTEL env vars injection", func(t *testing.T) {
		deployments := helm.UnmarshalStdout[appsv1.Deployment](t, e.withOtelEnvVars)
		injectorDeployment := findInjectorDeployment(deployments)
		require.NotNil(t, injectorDeployment, "dapr-sidecar-injector deployment should exist")
		containers := injectorDeployment.Spec.Template.Spec.Containers
		assert.NotEmpty(t, containers, "Sidecar injector should have at least one container")

		injectorContainer := containers[0]
		envMap := make(map[string]string)
		for _, envvar := range injectorContainer.Env {
			envMap[envvar.Name] = envvar.Value
		}

		// Verify OTEL env vars are set
		assert.Equal(t, "my-dapr-sidecar", envMap["OTEL_SERVICE_NAME"])
		assert.Equal(t, "k8s.pod.name=my-pod", envMap["OTEL_RESOURCE_ATTRIBUTES"])
		assert.Equal(t, "http://otel-collector-opentelemetry-collector.opentelemetry.svc.cluster.local:4317", envMap["OTEL_EXPORTER_OTLP_ENDPOINT"])
	})

	t.Run("escaped commas in env vars", func(t *testing.T) {
		deployments := helm.UnmarshalStdout[appsv1.Deployment](t, e.withEscapedCommas)
		injectorDeployment := findInjectorDeployment(deployments)
		require.NotNil(t, injectorDeployment, "dapr-sidecar-injector deployment should exist")
		containers := injectorDeployment.Spec.Template.Spec.Containers
		assert.NotEmpty(t, containers, "Sidecar injector should have at least one container")

		injectorContainer := containers[0]
		envMap := make(map[string]string)
		for _, envvar := range injectorContainer.Env {
			envMap[envvar.Name] = envvar.Value
		}

		// Verify escaped comma env vars are set correctly
		assert.Equal(t, "escaped-test", envMap["OTEL_SERVICE_NAME"])
		assert.Equal(t, "k8s.pod.name=my-pod,k8s.namespace.name=default,k8s.deployment.name=my-app", envMap["OTEL_RESOURCE_ATTRIBUTES"])
		assert.Equal(t, "http://otel-collector:4317", envMap["OTEL_EXPORTER_OTLP_ENDPOINT"])
	})

	t.Run("scheduler enabled", func(t *testing.T) {
		deployments := helm.UnmarshalStdout[appsv1.Deployment](t, e.base)
		injectorDeployment := findInjectorDeployment(deployments)
		require.NotNil(t, injectorDeployment)
		containers := injectorDeployment.Spec.Template.Spec.Containers
		assert.NotEmpty(t, containers)
		injectorContainer := containers[0]
		assert.Contains(t, injectorContainer.Args, "--scheduler-enabled=true")

		deployments = helm.UnmarshalStdout[appsv1.Deployment](t, e.withSchedulerEnabled)
		injectorDeployment = findInjectorDeployment(deployments)
		require.NotNil(t, injectorDeployment)
		containers = injectorDeployment.Spec.Template.Spec.Containers
		assert.NotEmpty(t, containers)
		injectorContainer = containers[0]
		assert.Contains(t, injectorContainer.Args, "--scheduler-enabled=true")

		deployments = helm.UnmarshalStdout[appsv1.Deployment](t, e.withSchedulerDisabled)
		injectorDeployment = findInjectorDeployment(deployments)
		require.NotNil(t, injectorDeployment)
		containers = injectorDeployment.Spec.Template.Spec.Containers
		assert.NotEmpty(t, containers)
		injectorContainer = containers[0]
		assert.Contains(t, injectorContainer.Args, "--scheduler-enabled=false")
	})
}
