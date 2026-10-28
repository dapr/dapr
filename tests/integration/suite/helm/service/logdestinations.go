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
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/helm"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() { suite.Register(new(logdestinations)) }

type logdestinations struct{ renders map[string]*helm.Helm }

func (l *logdestinations) Setup(t *testing.T) []framework.Option {
	l.renders = make(map[string]*helm.Helm)
	options := make([]framework.Option, 0, 4)
	for _, name := range []string{"defaults", "file", "outputs", "both"} {
		var values []string
		for _, component := range []string{"operator", "placement", "scheduler", "sentry", "sidecar_injector"} {
			if name == "file" || name == "both" {
				values = append(values, "dapr_"+component+".logFile="+strconv.Quote("/var/log/dapr/control plane.log"))
			}
			if name == "outputs" || name == "both" {
				values = append(values, "dapr_"+component+".logOutputs="+strconv.Quote("stdout,/var/log/dapr/extra.log"))
			}
		}
		rendered := helm.New(t, helm.WithSetJSON(values...))
		l.renders[name] = rendered
		options = append(options, framework.WithProcesses(rendered))
	}
	return options
}

func (l *logdestinations) Run(t *testing.T, _ context.Context) {
	// Deployment and StatefulSet share the pod-template structure used here.
	type workload struct {
		Kind string `json:"kind"`
		Spec struct {
			Template corev1.PodTemplateSpec `json:"template"`
		} `json:"spec"`
	}
	for name, rendered := range l.renders {
		t.Run(name, func(t *testing.T) {
			var containers []string
			for _, resource := range helm.UnmarshalStdout[workload](t, rendered) {
				if resource.Kind != "Deployment" && resource.Kind != "StatefulSet" {
					continue
				}
				for _, container := range resource.Spec.Template.Spec.Containers {
					containers = append(containers, container.Name)
					assertFlag := func(flag, expected string, enabled bool) {
						t.Helper()
						index := slices.Index(container.Args, flag)
						if !enabled {
							assert.Equal(t, -1, index)
							return
						}
						require.NotEqual(t, -1, index)
						require.Greater(t, len(container.Args), index+1)
						assert.Equal(t, expected, container.Args[index+1])
					}
					assertFlag("--log-file", "/var/log/dapr/control plane.log", name == "file" || name == "both")
					assertFlag("--log-outputs", "stdout,/var/log/dapr/extra.log", name == "outputs" || name == "both")
				}
			}
			assert.ElementsMatch(t, []string{"dapr-operator", "dapr-placement-server", "dapr-scheduler-server", "dapr-sentry", "dapr-sidecar-injector"}, containers)
		})
	}
}
