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
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

// basic tests the operator's ListCompontns API.
type basic struct {
	helm *helm.Helm

	helmStdout io.ReadCloser
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	stdoutR, stdoutW := io.Pipe()
	b.helmStdout = stdoutR
	b.helm = helm.New(t,
		helm.WithGlobalValues("ha.enabled=false"), // Not HA
		helm.WithShowOnlySchedulerSTS(),
		helm.WithStdout(stdoutW),
	)

	helmErrLogLine := logline.New(t,
		logline.WithStderrLineContains("values set in dapr_scheduler chart in .Values.replicaCount should be an odd number"),
	)
	helmErr := helm.New(t,
		helm.WithGlobalValues("ha.enabled=false"), // Not HA
		helm.WithValues("dapr_scheduler.replicaCount=4"),
		helm.WithExit1(),
		helm.WithStderr(helmErrLogLine.Stderr()),
	)

	return []framework.Option{
		framework.WithProcesses(b.helm, helmErrLogLine, helmErr),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	var sts appsv1.StatefulSet
	bs, err := io.ReadAll(b.helmStdout)
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(bs, &sts))
	require.Equal(t, int32(1), *sts.Spec.Replicas)
	require.NotNil(t, sts.Spec.Template.Spec.Affinity)
	require.Nil(t, sts.Spec.Template.Spec.Affinity.PodAntiAffinity)

	require.NotEmpty(t, sts.Spec.Template.Spec.Containers)
	var found bool
	for i, arg := range sts.Spec.Template.Spec.Containers[0].Args {
		if arg == "--replica-count" {
			found = true
			require.Greater(t, len(sts.Spec.Template.Spec.Containers[0].Args), i+1)
			assert.Equal(t, "1", sts.Spec.Template.Spec.Containers[0].Args[i+1])
		}
	}
	assert.True(t, found, "arg --replica-count not found in %v", sts.Spec.Template.Spec.Containers[0].Args)
}
