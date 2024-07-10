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
	suite.Register(new(basic))
}

// basic tests the operator's ListCompontns API.
type basic struct {
	helm    *helm.Helm
	helmErr *helm.Helm

	loglineHelmErr *logline.LogLine
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.loglineHelmErr = logline.New(t, logline.WithStderrLineContains("should be an odd number"))

	baseOpts := []helm.OptionFunc{
		helm.WithGlobalValues("ha.enabled=false"), // Not HA
		helm.WithShowOnlySchedulerSTS(),
		helm.WithLocalBuffForStdout(),
	}
	b.helm = helm.New(t, baseOpts...)
	b.helmErr = helm.New(t, append(baseOpts,
		helm.WithValues("dapr_scheduler.replicaCount=4"),
		helm.WithExecOptions(
			exec.WithExitCode(1),
			exec.WithRunError(func(t *testing.T, err error) {
				require.ErrorContains(t, err, "exit status 1")
			}),
			exec.WithStderr(b.loglineHelmErr.Stderr())))...)

	return []framework.Option{
		framework.WithProcesses(b.loglineHelmErr, b.helm, b.helmErr),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	t.Run("get_default_replicas", func(t *testing.T) {
		var sts appsv1.StatefulSet
		require.NoError(t, yaml.Unmarshal(b.helm.GetStdout(), &sts))
		require.Equal(t, int32(1), *sts.Spec.Replicas) // single replica
	})
}
