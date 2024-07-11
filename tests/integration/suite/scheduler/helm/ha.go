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
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(ha))
}

// basic tests the operator's ListCompontns API.
type ha struct {
	helm    *helm.Helm
	helmErr *helm.Helm

	loglineHelmErr *logline.LogLine
}

func (b *ha) Setup(t *testing.T) []framework.Option {
	b.loglineHelmErr = logline.New(t, logline.WithStderrLineContains("should be an odd number"))

	b.helm = helm.New(t,
		helm.WithGlobalValues("ha.enabled=true"),
		helm.WithShowOnlySchedulerSTS(),
		helm.WithLocalBuffForStdout())

	b.helmErr = helm.New(t,
		helm.WithGlobalValues("ha.enabled=true"),
		helm.WithValues("dapr_scheduler.replicaCount=4"),
		helm.WithExecOptions(
			exec.WithExitCode(1),
			exec.WithRunError(func(t *testing.T, err error) {
				require.ErrorContains(t, err, "exit status 1")
			}),
			exec.WithStderr(b.loglineHelmErr.Stderr())))

	return []framework.Option{
		framework.WithProcesses(b.loglineHelmErr, b.helm, b.helmErr),
	}
}

func (b *ha) Run(t *testing.T, ctx context.Context) {
	t.Run("get_default_replicas", func(t *testing.T) {
		var sts appsv1.StatefulSet
		require.NoError(t, yaml.Unmarshal(b.helm.GetStdout(), &sts))
		require.Equal(t, int32(3), *sts.Spec.Replicas)
	})
	t.Run("pod_antiaffinity_should_be_present", func(t *testing.T) {
		var sts appsv1.StatefulSet
		require.NoError(t, yaml.Unmarshal(b.helm.GetStdout(), &sts))
		require.NotNil(t, sts.Spec.Template.Spec.Affinity)
		require.NotNil(t, sts.Spec.Template.Spec.Affinity.PodAntiAffinity)
	})
	t.Run("arg_replica_count_should_be_3", func(t *testing.T) {
		var sts appsv1.StatefulSet
		require.NoError(t, yaml.Unmarshal(b.helm.GetStdout(), &sts))
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
}
