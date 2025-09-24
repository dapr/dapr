/*
Copyright 2024 The Dapr Authors
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

package helm

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/yaml"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/helm"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(ha))
}

type ha struct {
	helm           *helm.Helm
	helmNamespaced *helm.Helm
}

func (b *ha) Setup(t *testing.T) []framework.Option {
	b.helm = helm.New(t,
		helm.WithGlobalValues("ha.enabled=true"),
		helm.WithShowOnlySchedulerSTS(),
	)

	b.helmNamespaced = helm.New(t,
		helm.WithGlobalValues("ha.enabled=true"),
		helm.WithShowOnlySchedulerSTS(),
		helm.WithNamespace("dapr-system"),
	)

	return []framework.Option{
		framework.WithProcesses(b.helm, b.helmNamespaced),
	}
}

func (b *ha) Run(t *testing.T, ctx context.Context) {
	// good path
	var sts appsv1.StatefulSet
	bs, err := io.ReadAll(b.helm.Stdout(t))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(bs, &sts))

	t.Run("get_default_replicas", func(t *testing.T) {
		require.Equal(t, int32(3), *sts.Spec.Replicas)
	})

	t.Run("pod_antiaffinity_should_be_present", func(t *testing.T) {
		require.NotNil(t, sts.Spec.Template.Spec.Affinity)
		require.NotNil(t, sts.Spec.Template.Spec.Affinity.PodAntiAffinity)
	})

	t.Run("initial_cluster_has_all_instances_default", func(t *testing.T) {
		requireArgsValue(t, sts.Spec.Template.Spec.Containers[0].Args, "--etcd-initial-cluster",
			"dapr-scheduler-server-0=https://dapr-scheduler-server-0.dapr-scheduler-server.default.svc.cluster.local:2380,"+
				"dapr-scheduler-server-1=https://dapr-scheduler-server-1.dapr-scheduler-server.default.svc.cluster.local:2380,"+
				"dapr-scheduler-server-2=https://dapr-scheduler-server-2.dapr-scheduler-server.default.svc.cluster.local:2380")
	})

	t.Run("etcd_client_ports_default", func(t *testing.T) {
		requireArgsValue(t, sts.Spec.Template.Spec.Containers[0].Args, "--etcd-client-port", "2379")
	})

	// namespaced
	t.Run("initial_cluster_has_all_instances_dapr_system_namespace", func(t *testing.T) {
		var stsNamespaced appsv1.StatefulSet
		bs, err = io.ReadAll(b.helmNamespaced.Stdout(t))
		require.NoError(t, err)
		require.NoError(t, yaml.Unmarshal(bs, &stsNamespaced))
		requireArgsValue(t, stsNamespaced.Spec.Template.Spec.Containers[0].Args, "--etcd-initial-cluster",
			"dapr-scheduler-server-0=https://dapr-scheduler-server-0.dapr-scheduler-server.dapr-system.svc.cluster.local:2380,"+
				"dapr-scheduler-server-1=https://dapr-scheduler-server-1.dapr-scheduler-server.dapr-system.svc.cluster.local:2380,"+
				"dapr-scheduler-server-2=https://dapr-scheduler-server-2.dapr-scheduler-server.dapr-system.svc.cluster.local:2380")
	})
}

func requireArgsValue(t *testing.T, args []string, arg, value string) {
	t.Helper()
	for i, a := range args {
		if a == arg {
			if len(args) > i+1 {
				require.Equal(t, value, args[i+1])
				return
			}
		}
	}
	assert.Fail(t, "arg not found", arg)
}
