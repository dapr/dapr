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

package containers

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
	suite.Register(new(containers))
}

// containers tests that initContainers and extraContainers are correctly
// rendered in control plane Helm chart templates.
type containers struct {
	defaultOperator   *helm.Helm
	defaultPlacement  *helm.Helm
	defaultScheduler  *helm.Helm
	defaultSentry     *helm.Helm
	defaultInjector   *helm.Helm
	withInitOperator  *helm.Helm
	withInitPlacement *helm.Helm
	withInitScheduler *helm.Helm
	withInitSentry    *helm.Helm
	withInitInjector  *helm.Helm
	withExtraOperator *helm.Helm
	withExtraInjector *helm.Helm
	withBothSentry    *helm.Helm
}

func (c *containers) Setup(t *testing.T) []framework.Option {
	// Default configs (no initContainers or extraContainers)
	c.defaultOperator = helm.New(t,
		helm.WithShowOnly("charts/dapr_operator", "dapr_operator_deployment"),
	)
	c.defaultPlacement = helm.New(t,
		helm.WithShowOnly("charts/dapr_placement", "dapr_placement_statefulset"),
	)
	c.defaultScheduler = helm.New(t,
		helm.WithShowOnly("charts/dapr_scheduler", "dapr_scheduler_statefulset"),
	)
	c.defaultSentry = helm.New(t,
		helm.WithShowOnly("charts/dapr_sentry", "dapr_sentry_deployment"),
	)
	c.defaultInjector = helm.New(t,
		helm.WithShowOnly("charts/dapr_sidecar_injector", "dapr_sidecar_injector_deployment"),
	)

	// With initContainers
	c.withInitOperator = helm.New(t,
		helm.WithShowOnly("charts/dapr_operator", "dapr_operator_deployment"),
		helm.WithSetJSON(
			`global.initContainers.operator=[{"name":"init-certs","image":"busybox:1.36","command":["sh","-c","echo init"]}]`,
		),
	)
	c.withInitPlacement = helm.New(t,
		helm.WithShowOnly("charts/dapr_placement", "dapr_placement_statefulset"),
		helm.WithSetJSON(
			`global.initContainers.placement=[{"name":"init-data","image":"alpine:3.19","command":["sh","-c","echo ready"]}]`,
		),
	)
	c.withInitScheduler = helm.New(t,
		helm.WithShowOnly("charts/dapr_scheduler", "dapr_scheduler_statefulset"),
		helm.WithSetJSON(
			`global.initContainers.scheduler=[{"name":"init-config","image":"busybox:1.36","command":["sh","-c","echo config"]}]`,
		),
	)
	c.withInitSentry = helm.New(t,
		helm.WithShowOnly("charts/dapr_sentry", "dapr_sentry_deployment"),
		helm.WithSetJSON(
			`global.initContainers.sentry=[{"name":"init-pki","image":"busybox:1.36","command":["sh","-c","echo pki"]}]`,
		),
	)
	c.withInitInjector = helm.New(t,
		helm.WithShowOnly("charts/dapr_sidecar_injector", "dapr_sidecar_injector_deployment"),
		helm.WithSetJSON(
			`global.initContainers.injector=[{"name":"init-webhook","image":"busybox:1.36","command":["sh","-c","echo webhook"]}]`,
		),
	)

	// With extraContainers
	c.withExtraOperator = helm.New(t,
		helm.WithShowOnly("charts/dapr_operator", "dapr_operator_deployment"),
		helm.WithSetJSON(
			`global.extraContainers.operator=[{"name":"log-collector","image":"fluent/fluentd:v1.16","ports":[{"containerPort":24224}]}]`,
		),
	)
	c.withExtraInjector = helm.New(t,
		helm.WithShowOnly("charts/dapr_sidecar_injector", "dapr_sidecar_injector_deployment"),
		helm.WithSetJSON(
			`global.extraContainers.injector=[{"name":"metrics-proxy","image":"prom/pushgateway:v1.7","ports":[{"containerPort":9091}]}]`,
		),
	)

	// With both initContainers and extraContainers
	c.withBothSentry = helm.New(t,
		helm.WithShowOnly("charts/dapr_sentry", "dapr_sentry_deployment"),
		helm.WithSetJSON(
			`global.initContainers.sentry=[{"name":"init-pki","image":"busybox:1.36","command":["sh","-c","echo pki"]}]`,
			`global.extraContainers.sentry=[{"name":"audit-logger","image":"fluent/fluentd:v1.16"}]`,
		),
	)

	return []framework.Option{
		framework.WithProcesses(
			c.defaultOperator,
			c.defaultPlacement,
			c.defaultScheduler,
			c.defaultSentry,
			c.defaultInjector,
			c.withInitOperator,
			c.withInitPlacement,
			c.withInitScheduler,
			c.withInitSentry,
			c.withInitInjector,
			c.withExtraOperator,
			c.withExtraInjector,
			c.withBothSentry,
		),
	}
}

// findDeployment returns the first Deployment from the helm template output.
// Some templates (e.g. sentry) emit multiple YAML documents (Secret,
// ConfigMap, Deployment), so we filter by Kind rather than assuming a single
// document.
func findDeployment(t *testing.T, h *helm.Helm) appsv1.Deployment {
	t.Helper()
	all := helm.UnmarshalStdout[appsv1.Deployment](t, h)
	for _, d := range all {
		if d.Kind == "Deployment" {
			return d
		}
	}
	t.Fatal("no Deployment found in helm template output")
	return appsv1.Deployment{}
}

func (c *containers) Run(t *testing.T, ctx context.Context) {
	t.Run("default has no initContainers", func(t *testing.T) {
		dep := findDeployment(t, c.defaultOperator)
		assert.Empty(t, dep.Spec.Template.Spec.InitContainers,
			"operator should have no initContainers by default")

		dep = findDeployment(t, c.defaultSentry)
		assert.Empty(t, dep.Spec.Template.Spec.InitContainers,
			"sentry should have no initContainers by default")

		dep = findDeployment(t, c.defaultInjector)
		assert.Empty(t, dep.Spec.Template.Spec.InitContainers,
			"injector should have no initContainers by default")

		stss := helm.UnmarshalStdout[appsv1.StatefulSet](t, c.defaultPlacement)
		require.Len(t, stss, 1)
		assert.Empty(t, stss[0].Spec.Template.Spec.InitContainers,
			"placement should have no initContainers by default")

		stss = helm.UnmarshalStdout[appsv1.StatefulSet](t, c.defaultScheduler)
		require.Len(t, stss, 1)
		assert.Empty(t, stss[0].Spec.Template.Spec.InitContainers,
			"scheduler should have no initContainers by default")
	})

	t.Run("default has only one container per component", func(t *testing.T) {
		dep := findDeployment(t, c.defaultOperator)
		assert.Len(t, dep.Spec.Template.Spec.Containers, 1,
			"operator should have exactly one container by default")

		dep = findDeployment(t, c.defaultSentry)
		assert.Len(t, dep.Spec.Template.Spec.Containers, 1,
			"sentry should have exactly one container by default")

		dep = findDeployment(t, c.defaultInjector)
		assert.Len(t, dep.Spec.Template.Spec.Containers, 1,
			"injector should have exactly one container by default")

		stss := helm.UnmarshalStdout[appsv1.StatefulSet](t, c.defaultPlacement)
		require.Len(t, stss, 1)
		assert.Len(t, stss[0].Spec.Template.Spec.Containers, 1,
			"placement should have exactly one container by default")

		stss = helm.UnmarshalStdout[appsv1.StatefulSet](t, c.defaultScheduler)
		require.Len(t, stss, 1)
		assert.Len(t, stss[0].Spec.Template.Spec.Containers, 1,
			"scheduler should have exactly one container by default")
	})

	t.Run("initContainers on operator deployment", func(t *testing.T) {
		deployments := helm.UnmarshalStdout[appsv1.Deployment](t, c.withInitOperator)
		require.Len(t, deployments, 1)
		initContainers := deployments[0].Spec.Template.Spec.InitContainers
		require.Len(t, initContainers, 1)
		assert.Equal(t, "init-certs", initContainers[0].Name)
		assert.Equal(t, "busybox:1.36", initContainers[0].Image)
		assert.Equal(t, []string{"sh", "-c", "echo init"}, initContainers[0].Command)
	})

	t.Run("initContainers on placement statefulset", func(t *testing.T) {
		stss := helm.UnmarshalStdout[appsv1.StatefulSet](t, c.withInitPlacement)
		require.Len(t, stss, 1)
		initContainers := stss[0].Spec.Template.Spec.InitContainers
		require.Len(t, initContainers, 1)
		assert.Equal(t, "init-data", initContainers[0].Name)
		assert.Equal(t, "alpine:3.19", initContainers[0].Image)
	})

	t.Run("initContainers on scheduler statefulset", func(t *testing.T) {
		stss := helm.UnmarshalStdout[appsv1.StatefulSet](t, c.withInitScheduler)
		require.Len(t, stss, 1)
		initContainers := stss[0].Spec.Template.Spec.InitContainers
		require.Len(t, initContainers, 1)
		assert.Equal(t, "init-config", initContainers[0].Name)
		assert.Equal(t, "busybox:1.36", initContainers[0].Image)
	})

	t.Run("initContainers on sentry deployment", func(t *testing.T) {
		dep := findDeployment(t, c.withInitSentry)
		initContainers := dep.Spec.Template.Spec.InitContainers
		require.Len(t, initContainers, 1)
		assert.Equal(t, "init-pki", initContainers[0].Name)
		assert.Equal(t, "busybox:1.36", initContainers[0].Image)
	})

	t.Run("initContainers on injector deployment", func(t *testing.T) {
		deployments := helm.UnmarshalStdout[appsv1.Deployment](t, c.withInitInjector)
		require.Len(t, deployments, 1)
		initContainers := deployments[0].Spec.Template.Spec.InitContainers
		require.Len(t, initContainers, 1)
		assert.Equal(t, "init-webhook", initContainers[0].Name)
		assert.Equal(t, "busybox:1.36", initContainers[0].Image)
	})

	t.Run("extraContainers on operator deployment", func(t *testing.T) {
		deployments := helm.UnmarshalStdout[appsv1.Deployment](t, c.withExtraOperator)
		require.Len(t, deployments, 1)
		allContainers := deployments[0].Spec.Template.Spec.Containers
		require.Len(t, allContainers, 2, "should have main container plus extra container")

		// Find the extra container (not the main dapr-operator one)
		var extraContainer *int
		for i := range allContainers {
			if allContainers[i].Name == "log-collector" {
				extraContainer = &i
				break
			}
		}
		require.NotNil(t, extraContainer, "log-collector extra container should exist")
		assert.Equal(t, "fluent/fluentd:v1.16", allContainers[*extraContainer].Image)
		require.Len(t, allContainers[*extraContainer].Ports, 1)
		assert.Equal(t, int32(24224), allContainers[*extraContainer].Ports[0].ContainerPort)
	})

	t.Run("extraContainers on injector deployment", func(t *testing.T) {
		deployments := helm.UnmarshalStdout[appsv1.Deployment](t, c.withExtraInjector)
		require.Len(t, deployments, 1)
		allContainers := deployments[0].Spec.Template.Spec.Containers
		require.Len(t, allContainers, 2, "should have main container plus extra container")

		var found bool
		for _, c := range allContainers {
			if c.Name == "metrics-proxy" {
				found = true
				assert.Equal(t, "prom/pushgateway:v1.7", c.Image)
				require.Len(t, c.Ports, 1)
				assert.Equal(t, int32(9091), c.Ports[0].ContainerPort)
			}
		}
		assert.True(t, found, "metrics-proxy extra container should exist")
	})

	t.Run("both initContainers and extraContainers on sentry", func(t *testing.T) {
		dep := findDeployment(t, c.withBothSentry)

		// Verify initContainers
		initContainers := dep.Spec.Template.Spec.InitContainers
		require.Len(t, initContainers, 1)
		assert.Equal(t, "init-pki", initContainers[0].Name)
		assert.Equal(t, "busybox:1.36", initContainers[0].Image)

		// Verify extraContainers alongside main container
		allContainers := dep.Spec.Template.Spec.Containers
		require.Len(t, allContainers, 2, "should have main container plus extra container")

		var found bool
		for _, c := range allContainers {
			if c.Name == "audit-logger" {
				found = true
				assert.Equal(t, "fluent/fluentd:v1.16", c.Image)
			}
		}
		assert.True(t, found, "audit-logger extra container should exist")
	})
}
