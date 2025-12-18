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
	suite.Register(new(disabled))
}

type disabled struct {
	disable *helm.Helm
	enable  *helm.Helm
}

func (d *disabled) Setup(t *testing.T) []framework.Option {
	d.disable = helm.New(t,
		helm.WithValues("global.scheduler.enabled=false"),
	)

	d.enable = helm.New(t,
		helm.WithValues("global.scheduler.enabled=true"),
		helm.WithShowOnlySchedulerSTS(),
	)

	return []framework.Option{
		framework.WithProcesses(d.enable, d.disable),
	}
}

func (d *disabled) Run(t *testing.T, ctx context.Context) {
	var sts appsv1.StatefulSet
	bs, err := io.ReadAll(d.enable.Stdout(t))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(bs, &sts))

	bs, err = io.ReadAll(d.disable.Stdout(t))
	require.NoError(t, err)
	assert.NotContains(t, string(bs), "dapr_scheduler_statefulset")
}
