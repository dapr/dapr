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
	suite.Register(new(embed))
}

type embed struct {
	nonembed *helm.Helm
	embed    *helm.Helm
}

func (e *embed) Setup(t *testing.T) []framework.Option {
	e.nonembed = helm.New(t,
		helm.WithShowOnlySchedulerSTS(),
	)

	e.embed = helm.New(t,
		helm.WithValues(
			"dapr_scheduler.etcdEmbed=false",
			"dapr_scheduler.etcdClientEndpoints={localhost:1234,localhost:5678}",
			"dapr_scheduler.etcdClientUsername=my-username",
			"dapr_scheduler.etcdClientPassword=my-password",
		),
		helm.WithShowOnlySchedulerSTS(),
	)

	return []framework.Option{
		framework.WithProcesses(e.nonembed, e.embed),
	}
}

func (e *embed) Run(t *testing.T, ctx context.Context) {
	var sts appsv1.StatefulSet

	bs, err := io.ReadAll(e.nonembed.Stdout(t))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(bs, &sts))
	requireArgsBoolValue(t, sts.Spec.Template.Spec.Containers[0].Args, "--etcd-embed=true", "true")
	assert.NotContains(t, sts.Spec.Template.Spec.Containers[0].Args, "--etcd-client-endpoints")
	assert.NotContains(t, sts.Spec.Template.Spec.Containers[0].Args, "--etcd-client-username")
	assert.NotContains(t, sts.Spec.Template.Spec.Containers[0].Args, "--etcd-client-password")

	bs, err = io.ReadAll(e.embed.Stdout(t))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(bs, &sts))
	requireArgsBoolValue(t, sts.Spec.Template.Spec.Containers[0].Args, "--etcd-embed=false", "false")
	requireArgsValue(t, sts.Spec.Template.Spec.Containers[0].Args, "--etcd-client-endpoints", "localhost:1234,localhost:5678")
	requireArgsValue(t, sts.Spec.Template.Spec.Containers[0].Args, "--etcd-client-username", "my-username")
	requireArgsValue(t, sts.Spec.Template.Spec.Containers[0].Args, "--etcd-client-password", "my-password")
}
