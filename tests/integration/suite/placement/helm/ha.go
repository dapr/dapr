/*
Copyright 2026 The Dapr Authors
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
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(ha))
}

type ha struct {
	helm *helm.Helm
}

func (b *ha) Setup(t *testing.T) []framework.Option {
	b.helm = helm.New(t,
		helm.WithShowOnly("charts/dapr_placement", "dapr_placement_statefulset.yaml"),
		helm.WithGlobalValues("ha.enabled=true"),
	)

	return []framework.Option{
		framework.WithProcesses(b.helm),
	}
}

func (b *ha) Run(t *testing.T, ctx context.Context) {
	var sts appsv1.StatefulSet
	bs, err := io.ReadAll(b.helm.Stdout(t))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(bs, &sts))

	t.Run("get_default_replicas", func(t *testing.T) {
		require.Equal(t, int32(3), *sts.Spec.Replicas)
	})

	t.Run("pod_antiaffinity_should_be_present", func(t *testing.T) {
		require.NotNil(t, sts.Spec.Template.Spec.Affinity)
		require.NotNil(t, sts.Spec.Template.Spec.Affinity.PodAntiAffinity)
	})

	t.Run("affinity_can_be_customized", func(t *testing.T) {
		helmCustom := helm.New(t,
			helm.WithShowOnly("charts/dapr_placement", "dapr_placement_statefulset.yaml"),
			helm.WithGlobalValues("ha.enabled=true"),
			helm.WithValues(
				"dapr_placement.affinity.podAntiAffinity.requiredDuringSchedulingIgnoredDuringExecution[0].topologyKey=topology.kubernetes.io/zone",
			),
		)
		helmCustom.Run(t, ctx)
		defer helmCustom.Cleanup(t)

		var stsCustom appsv1.StatefulSet
		var bsCustom []byte
		bsCustom, err = io.ReadAll(helmCustom.Stdout(t))
		require.NoError(t, err)
		require.NoError(t, yaml.Unmarshal(bsCustom, &stsCustom))
		require.NotNil(t, stsCustom.Spec.Template.Spec.Affinity)
		require.NotNil(t, stsCustom.Spec.Template.Spec.Affinity.PodAntiAffinity)
		require.NotEmpty(t, stsCustom.Spec.Template.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution)
	})
}
