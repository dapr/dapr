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

package containers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/helm"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(securityContext))
}

type securityContext struct {
	defaults   *helm.Helm
	configured *helm.Helm
	localOnly  *helm.Helm
	windows    *helm.Helm
}

func (s *securityContext) Setup(t *testing.T) []framework.Option {
	render := func(opts ...helm.OptionFunc) *helm.Helm {
		return helm.New(t, append([]helm.OptionFunc{
			helm.WithShowOnly("charts/dapr_*", "dapr_*_deployment"),
			helm.WithShowOnlyPlacementSTS(),
			helm.WithShowOnlySchedulerSTS(),
		}, opts...)...)
	}
	global := helm.WithSetJSON(`global.securityContext={"runAsUser":30000,"runAsGroup":30001,"seLinuxOptions":{"type":"container_t","level":"s0:c1,c2"}}`)
	s.defaults = render()
	s.configured = render(global,
		helm.WithGlobalValues("seccompProfile=RuntimeDefault"),
		helm.WithValues("dapr_placement.fsGroup=null", "dapr_scheduler.securityContext.fsGroup=0"),
		helm.WithSetJSON(
			`dapr_sentry.securityContext={"runAsUser":31000,"seLinuxOptions":{"level":"s0:c3,c4"}}`,
			`dapr_operator.securityContext={"runAsGroup":null,"seLinuxOptions":{}}`,
		),
	)
	s.localOnly = render(
		helm.WithValues(
			"dapr_sentry.runAsNonRoot=false",
			"dapr_sentry.securityContext.runAsUser=0",
			"dapr_sentry.securityContext.runAsGroup=0",
			"dapr_placement.fsGroup=0",
			"dapr_scheduler.securityContext.fsGroup=null",
		),
	)
	s.windows = render(global, helm.WithGlobalValues("daprControlPlaneOs=windows"))

	return []framework.Option{
		framework.WithProcesses(s.defaults, s.configured, s.localOnly, s.windows),
	}
}

func (s *securityContext) Run(t *testing.T, ctx context.Context) {
	for _, tc := range []struct {
		name        string
		render      *helm.Helm
		placementFS *int64
		schedulerFS *int64
	}{
		{"defaults", s.defaults, new(int64(65532)), new(int64(65532))},
		{"configured", s.configured, nil, new(int64(0))},
		{"local only", s.localOnly, new(int64(0)), nil},
		{"windows", s.windows, new(int64(65532)), new(int64(65532))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			type workload struct {
				Kind string
				Spec struct {
					Template corev1.PodTemplateSpec
				}
			}
			pods := make(map[string]corev1.PodSpec)
			for _, doc := range helm.UnmarshalStdout[workload](t, tc.render) {
				if doc.Kind == "Deployment" || doc.Kind == "StatefulSet" {
					pod := doc.Spec.Template.Spec
					require.Len(t, pod.Containers, 1)
					pods[pod.Containers[0].Name] = pod
				}
			}
			for _, name := range []string{"dapr-operator", "dapr-sentry", "dapr-sidecar-injector", "dapr-placement-server", "dapr-scheduler-server"} {
				t.Run(name, func(t *testing.T) {
					pod, ok := pods[name]
					require.True(t, ok, "workload must be rendered")

					var fsGroup *int64
					switch name {
					case "dapr-placement-server":
						fsGroup = tc.placementFS
					case "dapr-scheduler-server":
						fsGroup = tc.schedulerFS
					}
					if fsGroup == nil {
						assert.Nil(t, pod.SecurityContext, "pod security context")
					} else if assert.NotNil(t, pod.SecurityContext, "pod security context") {
						assert.Equal(t, fsGroup, pod.SecurityContext.FSGroup, "pod fsGroup")
					}

					got := pod.Containers[0].SecurityContext
					if tc.render == s.windows {
						assert.Nil(t, got, "Windows container security context")
						return
					}
					require.NotNil(t, got)
					want := corev1.SecurityContext{
						RunAsNonRoot:             new(true),
						AllowPrivilegeEscalation: new(false),
						ReadOnlyRootFilesystem:   new(true),
						Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
					}
					if tc.render == s.configured {
						want.RunAsUser = new(int64(30000))
						want.RunAsGroup = new(int64(30001))
						want.SELinuxOptions = &corev1.SELinuxOptions{Type: "container_t", Level: "s0:c1,c2"}
						want.SeccompProfile = &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}
						switch name {
						case "dapr-sentry":
							want.RunAsUser = new(int64(31000))
							want.SELinuxOptions = &corev1.SELinuxOptions{Level: "s0:c3,c4"}
						case "dapr-operator":
							want.RunAsGroup = nil
							want.SELinuxOptions = &corev1.SELinuxOptions{}
						}
					}
					if tc.render == s.localOnly && name == "dapr-sentry" {
						want.RunAsNonRoot = new(false)
						want.RunAsUser = new(int64(0))
						want.RunAsGroup = new(int64(0))
					}
					assert.Equal(t, want.RunAsUser, got.RunAsUser, "runAsUser")
					assert.Equal(t, want.RunAsGroup, got.RunAsGroup, "runAsGroup")
					assert.Equal(t, want.SELinuxOptions, got.SELinuxOptions, "seLinuxOptions")
					assert.Equal(t, want.RunAsNonRoot, got.RunAsNonRoot, "runAsNonRoot")
					assert.Equal(t, want.AllowPrivilegeEscalation, got.AllowPrivilegeEscalation, "allowPrivilegeEscalation")
					assert.Equal(t, want.ReadOnlyRootFilesystem, got.ReadOnlyRootFilesystem, "readOnlyRootFilesystem")
					assert.Equal(t, want.Capabilities, got.Capabilities, "capabilities")
					assert.Equal(t, want.SeccompProfile, got.SeccompProfile, "seccompProfile")
				})
			}
		})
	}
}
