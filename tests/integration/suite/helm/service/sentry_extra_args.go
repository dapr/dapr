/*
Copyright 2026 The Dapr Authors
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
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/helm"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(sentryExtraArgs))
}

// sentryExtraArgs verifies that the dapr_sentry.extraArgs Helm value is
// appended to the sentry container's args, and that the default ([]) leaves
// the built-in args untouched.
type sentryExtraArgs struct {
	base          *helm.Helm
	withExtraArgs *helm.Helm
}

func (e *sentryExtraArgs) Setup(t *testing.T) []framework.Option {
	e.base = helm.New(t,
		helm.WithShowOnlySentryDeployment(),
	)

	e.withExtraArgs = helm.New(t,
		helm.WithShowOnlySentryDeployment(),
		helm.WithSetJSON(
			`dapr_sentry.extraArgs=["--jwt-ttl=48h","--healthz-port=8081"]`,
		),
	)

	return []framework.Option{
		framework.WithProcesses(
			e.base,
			e.withExtraArgs,
		),
	}
}

func (e *sentryExtraArgs) Run(t *testing.T, ctx context.Context) {
	// The sentry template emits multiple documents (Secret, ConfigMap,
	// Deployment), so select the Deployment by Kind rather than assuming a
	// single document.
	sentryDeployment := func(t *testing.T, h *helm.Helm) appsv1.Deployment {
		t.Helper()
		for _, d := range helm.UnmarshalStdout[appsv1.Deployment](t, h) {
			if d.Kind == "Deployment" {
				return d
			}
		}
		t.Fatal("no sentry Deployment found in helm template output")
		return appsv1.Deployment{}
	}

	t.Run("default has no extra args", func(t *testing.T) {
		dep := sentryDeployment(t, e.base)
		containers := dep.Spec.Template.Spec.Containers
		require.Len(t, containers, 1, "sentry should have exactly one container by default")
		args := containers[0].Args
		assert.NotContains(t, args, "--jwt-ttl=48h")
		assert.NotContains(t, args, "--healthz-port=8081")
		// Built-in args are still rendered.
		assert.Contains(t, args, "--trust-domain")
	})

	t.Run("extra args appended to sentry container", func(t *testing.T) {
		dep := sentryDeployment(t, e.withExtraArgs)
		containers := dep.Spec.Template.Spec.Containers
		require.Len(t, containers, 1, "sentry should have exactly one container")
		args := containers[0].Args

		assert.Contains(t, args, "--jwt-ttl=48h")
		assert.Contains(t, args, "--healthz-port=8081")

		// Built-in args are preserved, and extraArgs are appended after them.
		require.Contains(t, args, "--trust-domain")
		assert.Greater(t, slices.Index(args, "--jwt-ttl=48h"), slices.Index(args, "--trust-domain"),
			"extraArgs should be appended after the built-in args")
	})
}
