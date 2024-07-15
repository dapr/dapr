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

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/yaml"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/helm"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(ha))
}

type ha struct {
	helm           *helm.Helm
	helmNamespaced *helm.Helm
	helmDisabled   *helm.Helm

	helmStdout           io.ReadCloser
	helmNamespacedStdout io.ReadCloser
	helmDisableStdout    io.ReadCloser
}

func (b *ha) Setup(t *testing.T) []framework.Option {
	stdoutR, stdoutW := io.Pipe()
	b.helmStdout = stdoutR
	b.helm = helm.New(t,
		helm.WithGlobalValues("ha.enabled=true"),
		helm.WithShowOnlySchedulerSTS(),
		helm.WithStdout(stdoutW),
	)

	stdoutRDisabled, stdoutWDisabled := io.Pipe()
	b.helmDisableStdout = stdoutRDisabled
	b.helmDisabled = helm.New(t,
		helm.WithGlobalValues("ha.enabled=true"),
		helm.WithValues("dapr_scheduler.replicaCount=0"),
		helm.WithShowOnlySchedulerSTS(),
		helm.WithStdout(stdoutWDisabled),
	)

	stdoutRNamespaced, stdoutWNamespaced := io.Pipe()
	b.helmNamespacedStdout = stdoutRNamespaced
	b.helmNamespaced = helm.New(t,
		helm.WithGlobalValues("ha.enabled=true"),
		helm.WithShowOnlySchedulerSTS(),
		helm.WithNamespace("dapr-system"),
		helm.WithStdout(stdoutWNamespaced),
	)

	helmErrLogLine := logline.New(t,
		logline.WithStderrLineContains("values set in dapr_scheduler chart in .Values.replicaCount should be an odd numbe"),
	)
	helmErr := helm.New(t,
		helm.WithGlobalValues("ha.enabled=true"),
		helm.WithValues("dapr_scheduler.replicaCount=4"),
		helm.WithExit1(),
		helm.WithStderr(helmErrLogLine.Stderr()),
	)

	return []framework.Option{
		framework.WithProcesses(b.helm, b.helmDisabled, b.helmNamespaced, helmErrLogLine, helmErr),
	}
}

func (b *ha) Run(t *testing.T, ctx context.Context) {
	// good path
	var sts appsv1.StatefulSet
	bs, err := io.ReadAll(b.helmStdout)
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(bs, &sts))

	t.Run("get_default_replicas", func(t *testing.T) {
		require.Equal(t, int32(3), *sts.Spec.Replicas)
	})

	t.Run("pod_antiaffinity_should_be_present", func(t *testing.T) {
		require.NotNil(t, sts.Spec.Template.Spec.Affinity)
		require.NotNil(t, sts.Spec.Template.Spec.Affinity.PodAntiAffinity)
	})
	t.Run("arg_replica_count_should_be_3", func(t *testing.T) {
		requireArgsValue(t, sts.Spec.Template.Spec.Containers[0].Args, "--replica-count", "3")
	})

	t.Run("initial_cluster_has_all_instances_default", func(t *testing.T) {
		requireArgsValue(t, sts.Spec.Template.Spec.Containers[0].Args, "--initial-cluster",
			"dapr-scheduler-server-0=http://dapr-scheduler-server-0.dapr-scheduler-server.default.svc.cluster.local:2380,"+
				"dapr-scheduler-server-1=http://dapr-scheduler-server-1.dapr-scheduler-server.default.svc.cluster.local:2380,"+
				"dapr-scheduler-server-2=http://dapr-scheduler-server-2.dapr-scheduler-server.default.svc.cluster.local:2380")
	})

	t.Run("etcd_client_ports_default", func(t *testing.T) {
		requireArgsValue(t, sts.Spec.Template.Spec.Containers[0].Args, "--etcd-client-ports", "dapr-scheduler-server-0=2379,dapr-scheduler-server-1=2379,dapr-scheduler-server-2=2379")
		requireArgsValue(t, sts.Spec.Template.Spec.Containers[0].Args, "--etcd-client-http-ports", "dapr-scheduler-server-0=2330,dapr-scheduler-server-1=2330,dapr-scheduler-server-2=2330")
	})

	// disabled scheduler (replica count 0)
	t.Run("get_default_replicas_disabled", func(t *testing.T) {
		var sts appsv1.StatefulSet
		bs, err = io.ReadAll(b.helmDisableStdout)
		require.NoError(t, err)
		require.NoError(t, yaml.Unmarshal(bs, &sts))
		require.NotNil(t, *sts.Spec.Replicas)
		require.Equal(t, int32(0), *sts.Spec.Replicas)
		requireArgsValue(t, sts.Spec.Template.Spec.Containers[0].Args, "--replica-count", "0")
		requireArgsValue(t, sts.Spec.Template.Spec.Containers[0].Args, "--initial-cluster", "")
		requireArgsValue(t, sts.Spec.Template.Spec.Containers[0].Args, "--etcd-client-ports", "")
		requireArgsValue(t, sts.Spec.Template.Spec.Containers[0].Args, "--etcd-client-http-ports", "")
	})

	// namespaced
	t.Run("initial_cluster_has_all_instances_dapr_system_namespace", func(t *testing.T) {
		var stsNamespaced appsv1.StatefulSet
		bs, err = io.ReadAll(b.helmNamespacedStdout)
		require.NoError(t, err)
		require.NoError(t, yaml.Unmarshal(bs, &stsNamespaced))
		requireArgsValue(t, stsNamespaced.Spec.Template.Spec.Containers[0].Args, "--initial-cluster",
			"dapr-scheduler-server-0=http://dapr-scheduler-server-0.dapr-scheduler-server.dapr-system.svc.cluster.local:2380,"+
				"dapr-scheduler-server-1=http://dapr-scheduler-server-1.dapr-scheduler-server.dapr-system.svc.cluster.local:2380,"+
				"dapr-scheduler-server-2=http://dapr-scheduler-server-2.dapr-scheduler-server.dapr-system.svc.cluster.local:2380")
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
	require.Failf(t, "arg %s not found", arg)
}
