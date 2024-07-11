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
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/yaml"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/helm"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(ha))
}

// basic tests the operator's ListCompontns API.
type ha struct {
	helm           *helm.Helm
	helmErr        *helm.Helm
	helmNamespaced *helm.Helm
}

func (b *ha) Setup(t *testing.T) []framework.Option {
	b.helm = helm.New(t,
		helm.WithGlobalValues("ha.enabled=true"),
		helm.WithShowOnlySchedulerSTS(),
		helm.WithLocalBuffForStdout())

	b.helmNamespaced = helm.New(t,
		helm.WithGlobalValues("ha.enabled=true"),
		helm.WithShowOnlySchedulerSTS(),
		helm.WithLocalBuffForStdout(),
		helm.WithNamespace("dapr-system"))

	b.helmErr = helm.New(t,
		helm.WithGlobalValues("ha.enabled=true"),
		helm.WithValues("dapr_scheduler.replicaCount=4"),
		helm.WithLocalBuffForStderr(),
		helm.WithExecOptions(
			exec.WithExitCode(1),
			exec.WithRunError(func(t *testing.T, err error) {
				require.ErrorContains(t, err, "exit status 1")
			}),
		))

	return []framework.Option{
		framework.WithProcesses(b.helm, b.helmErr, b.helmNamespaced),
	}
}

func (b *ha) Run(t *testing.T, ctx context.Context) {
	// good path
	var sts appsv1.StatefulSet
	require.NoError(t, yaml.Unmarshal(b.helm.GetStdout(), &sts))

	t.Run("get_default_replicas", func(t *testing.T) {
		require.Equal(t, int32(3), *sts.Spec.Replicas)
	})
	t.Run("pod_antiaffinity_should_be_present", func(t *testing.T) {
		require.NotNil(t, sts.Spec.Template.Spec.Affinity)
		require.NotNil(t, sts.Spec.Template.Spec.Affinity.PodAntiAffinity)
	})
	t.Run("arg_replica_count_should_be_3", func(t *testing.T) {
		var replicaArgFound bool
		for i, e := range sts.Spec.Template.Spec.Containers[0].Args {
			if e == "--replica-count" {
				require.Greater(t, len(sts.Spec.Template.Spec.Containers[0].Args), i+1)
				require.Equal(t, "3", sts.Spec.Template.Spec.Containers[0].Args[i+1])
				replicaArgFound = true
				break
			}
		}
		require.True(t, replicaArgFound)
	})
	t.Run("initial_cluster_has_all_instances_default", func(t *testing.T) {
		for i, e := range sts.Spec.Template.Spec.Containers[0].Args {
			if e == "--initial-cluster" {
				require.Equal(t,
					"dapr-scheduler-server-0=http://dapr-scheduler-server-0.dapr-scheduler-server.default.svc.cluster.local:2380,"+
						"dapr-scheduler-server-1=http://dapr-scheduler-server-1.dapr-scheduler-server.default.svc.cluster.local:2380,"+
						"dapr-scheduler-server-2=http://dapr-scheduler-server-2.dapr-scheduler-server.default.svc.cluster.local:2380", sts.Spec.Template.Spec.Containers[0].Args[i+1])
			}
		}
	})
	t.Run("etcd_client_ports_default", func(t *testing.T) {
		for i, e := range sts.Spec.Template.Spec.Containers[0].Args {
			if e == "--etcd-client-ports" {
				require.Equal(t, "dapr-scheduler-server-0=2379,dapr-scheduler-server-1=2379,dapr-scheduler-server-2=2379", sts.Spec.Template.Spec.Containers[0].Args[i+1])
			}
			if e == "--etcd-client-http-ports" {
				require.Equal(t, "dapr-scheduler-server-0=2330,dapr-scheduler-server-1=2330,dapr-scheduler-server-2=2330", sts.Spec.Template.Spec.Containers[0].Args[i+1])
			}
		}
	})

	// namespaced
	t.Run("initial_cluster_has_all_instances_dapr_system_namespace", func(t *testing.T) {
		var stsNamespaced appsv1.StatefulSet
		require.NoError(t, yaml.Unmarshal(b.helmNamespaced.GetStdout(), &stsNamespaced))
		for i, e := range stsNamespaced.Spec.Template.Spec.Containers[0].Args {
			if e == "--initial-cluster" {
				require.Equal(t,
					"dapr-scheduler-server-0=http://dapr-scheduler-server-0.dapr-scheduler-server.dapr-system.svc.cluster.local:2380"+
						",dapr-scheduler-server-1=http://dapr-scheduler-server-1.dapr-scheduler-server.dapr-system.svc.cluster.local:2380"+
						",dapr-scheduler-server-2=http://dapr-scheduler-server-2.dapr-scheduler-server.dapr-system.svc.cluster.local:2380", stsNamespaced.Spec.Template.Spec.Containers[0].Args[i+1])
			}
		}
	})

	// bad path helmerr
	t.Run("get_error_on_even_replicas", func(t *testing.T) {
		require.Contains(t, string(b.helmErr.GetStderr()), "should be an odd number")
	})
}
