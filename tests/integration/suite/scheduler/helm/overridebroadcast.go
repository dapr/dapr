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
	suite.Register(new(overrideBroadcastHostPort))
}

type overrideBroadcastHostPort struct {
	helmDefault  *helm.Helm
	helmOverride *helm.Helm
}

func (o *overrideBroadcastHostPort) Setup(t *testing.T) []framework.Option {
	o.helmDefault = helm.New(t,
		helm.WithShowOnlySchedulerSTS(),
	)

	o.helmOverride = helm.New(t,
		helm.WithShowOnlySchedulerSTS(),
		helm.WithValues("dapr_scheduler.overrideBroadcastHostPort=my-tls-gateway.example.com:50006"),
	)

	return []framework.Option{
		framework.WithProcesses(o.helmDefault, o.helmOverride),
	}
}

func (o *overrideBroadcastHostPort) Run(t *testing.T, ctx context.Context) {
	var sts appsv1.StatefulSet

	// Flag must be absent when not configured
	bs, err := io.ReadAll(o.helmDefault.Stdout(t))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(bs, &sts))
	for _, arg := range sts.Spec.Template.Spec.Containers[0].Args {
		assert.NotContains(t, arg, "--override-broadcast-host-port")
	}

	// Flag must be present with the correct value when configured
	bs, err = io.ReadAll(o.helmOverride.Stdout(t))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(bs, &sts))
	assert.Contains(t, sts.Spec.Template.Spec.Containers[0].Args,
		"--override-broadcast-host-port=my-tls-gateway.example.com:50006")
}
