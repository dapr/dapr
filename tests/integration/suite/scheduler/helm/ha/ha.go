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

package ha

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/yaml"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/helmtpl"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(helm))
}

// basic tests the operator's ListCompontns API.
type helm struct {
	helm *helmtpl.Helm
}

func (b *helm) Setup(t *testing.T) []framework.Option {
	b.helm = helmtpl.New(t,
		helmtpl.WithGlobalValues("ha.enabled=true"),
		helmtpl.WithShowOnlySchedulerSTS())
return []framework.Option{
		framework.WithProcesses(b.helm),
	}
}

func (b *helm) Run(t *testing.T, ctx context.Context) {
	t.Run("get_default_replicas", func(t *testing.T) {
		ymlBytes := b.helm.Render(t, ctx)
		var sts appsv1.StatefulSet
		require.NoError(t, yaml.Unmarshal(ymlBytes, &sts))
		require.Equal(t, int32(3), *sts.Spec.Replicas)
	})
	t.Run("get_error_message_if_replicas_even_value", func(t *testing.T) {
		_ = b.helm.Render(t, ctx,
			helmtpl.WithValues("dapr_scheduler.ha.replicaCount=2"),
			helmtpl.WithExitCode(1),
			helmtpl.WithExitErrorMsgRegex(`when in HA modes values .+ should be an odd number`))
	})
}
