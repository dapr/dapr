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
	suite.Register(new(workers))
}

type workers struct {
	helmDefault *helm.Helm
	workersLess *helm.Helm
	workersMore *helm.Helm
}

func (w *workers) Setup(t *testing.T) []framework.Option {
	w.helmDefault = helm.New(t,
		helm.WithShowOnlySchedulerSTS(),
	)

	w.workersLess = helm.New(t,
		helm.WithShowOnlySchedulerSTS(),
		helm.WithValues("dapr_scheduler.workers=10"),
	)

	w.workersMore = helm.New(t,
		helm.WithShowOnlySchedulerSTS(),
		helm.WithValues("dapr_scheduler.workers=1000"),
	)

	return []framework.Option{
		framework.WithProcesses(w.helmDefault, w.workersLess, w.workersMore),
	}
}

func (w *workers) Run(t *testing.T, ctx context.Context) {
	var sts appsv1.StatefulSet

	bs, err := io.ReadAll(w.helmDefault.Stdout(t))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(bs, &sts))
	assert.Contains(t, sts.Spec.Template.Spec.Containers[0].Args, "--workers=2048")

	bs, err = io.ReadAll(w.workersLess.Stdout(t))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(bs, &sts))
	assert.Contains(t, sts.Spec.Template.Spec.Containers[0].Args, "--workers=10")

	bs, err = io.ReadAll(w.workersMore.Stdout(t))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(bs, &sts))
	assert.Contains(t, sts.Spec.Template.Spec.Containers[0].Args, "--workers=1000")
}
