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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/helm"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(volumeClaimTemplate))
}

// volumeClaimTemplate verifies that the placement StatefulSet renders the
// raft-log volumeClaimTemplate and its mount the same way whether HA is
// enabled or not, so that toggling HA on an existing release only changes
// `replicas` and never touches `volumeClaimTemplates`, which Kubernetes
// cannot apply as an update to a live StatefulSet.
type volumeClaimTemplate struct {
	nonHA         *helm.Helm
	ha            *helm.Helm
	nonHAInMemory *helm.Helm
	haInMemory    *helm.Helm
}

func (v *volumeClaimTemplate) Setup(t *testing.T) []framework.Option {
	v.nonHA = helm.New(t,
		helm.WithShowOnlyPlacementSTS(),
		helm.WithGlobalValues("ha.enabled=false"),
	)
	v.ha = helm.New(t,
		helm.WithShowOnlyPlacementSTS(),
		helm.WithGlobalValues("ha.enabled=true"),
	)
	v.nonHAInMemory = helm.New(t,
		helm.WithGlobalValues("ha.enabled=false"),
		helm.WithShowOnlyPlacementSTS(),
		helm.WithValues("dapr_placement.cluster.forceInMemoryLog=true"),
	)
	v.haInMemory = helm.New(t,
		helm.WithGlobalValues("ha.enabled=true"),
		helm.WithShowOnlyPlacementSTS(),
		helm.WithValues("dapr_placement.cluster.forceInMemoryLog=true"),
	)

	return []framework.Option{
		framework.WithProcesses(v.nonHA, v.ha, v.nonHAInMemory, v.haInMemory),
	}
}

func (v *volumeClaimTemplate) Run(t *testing.T, ctx context.Context) {
	t.Run("volume_claim_template_is_identical_regardless_of_ha", func(t *testing.T) {
		nonHASTS := helm.UnmarshalStdout[appsv1.StatefulSet](t, v.nonHA)
		require.Len(t, nonHASTS, 1)
		haSTS := helm.UnmarshalStdout[appsv1.StatefulSet](t, v.ha)
		require.Len(t, haSTS, 1)

		require.Len(t, nonHASTS[0].Spec.VolumeClaimTemplates, 1)
		require.Equal(t, "raft-log", nonHASTS[0].Spec.VolumeClaimTemplates[0].Name)

		// The set of volumeClaimTemplates must not depend on the HA flag:
		// `spec.volumeClaimTemplates` is immutable on a live StatefulSet, so a
		// helm upgrade that only toggles `ha.enabled` must not change it.
		assert.Equal(t, nonHASTS[0].Spec.VolumeClaimTemplates, haSTS[0].Spec.VolumeClaimTemplates)
	})

	t.Run("volume_mount_is_rendered_regardless_of_ha", func(t *testing.T) {
		nonHASTS := helm.UnmarshalStdout[appsv1.StatefulSet](t, v.nonHA)
		require.Len(t, nonHASTS, 1)
		haSTS := helm.UnmarshalStdout[appsv1.StatefulSet](t, v.ha)
		require.Len(t, haSTS, 1)

		require.Len(t, nonHASTS[0].Spec.Template.Spec.Containers, 1)
		require.Len(t, haSTS[0].Spec.Template.Spec.Containers, 1)

		nonHAMount := raftLogMount(t, nonHASTS[0])
		haMount := raftLogMount(t, haSTS[0])
		assert.Equal(t, nonHAMount, haMount)
	})

	t.Run("no_volume_claim_template_when_force_in_memory_log", func(t *testing.T) {
		nonHASTS := helm.UnmarshalStdout[appsv1.StatefulSet](t, v.nonHAInMemory)
		require.Len(t, nonHASTS, 1)
		haSTS := helm.UnmarshalStdout[appsv1.StatefulSet](t, v.haInMemory)
		require.Len(t, haSTS, 1)

		assert.Empty(t, nonHASTS[0].Spec.VolumeClaimTemplates)
		assert.Empty(t, haSTS[0].Spec.VolumeClaimTemplates)

		for _, vm := range nonHASTS[0].Spec.Template.Spec.Containers[0].VolumeMounts {
			assert.NotEqual(t, "raft-log", vm.Name)
		}
		for _, vm := range haSTS[0].Spec.Template.Spec.Containers[0].VolumeMounts {
			assert.NotEqual(t, "raft-log", vm.Name)
		}
	})
}

func raftLogMount(t *testing.T, sts appsv1.StatefulSet) corev1.VolumeMount {
	t.Helper()

	for _, vm := range sts.Spec.Template.Spec.Containers[0].VolumeMounts {
		if vm.Name == "raft-log" {
			return vm
		}
	}

	require.Fail(t, "raft-log volume mount not found")
	return corev1.VolumeMount{}
}
