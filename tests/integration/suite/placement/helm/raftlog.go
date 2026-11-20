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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/yaml"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/helm"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(raftLog))
}

// raftLog asserts the raft-log volume claim, mount and log store path that a
// fresh install renders. On an upgrade the chart takes the claim from the live
// StatefulSet instead, which an offline render cannot see.
type raftLog struct {
	nonHA      *helm.Helm
	ha         *helm.Helm
	placeHA    *helm.Helm
	haInMem    *helm.Helm
	nonHAInMem *helm.Helm
}

func (p *raftLog) Setup(t *testing.T) []framework.Option {
	p.nonHA = helm.New(t, helm.WithShowOnlyPlacementSTS())
	p.ha = helm.New(t,
		helm.WithGlobalValues("ha.enabled=true"),
		helm.WithShowOnlyPlacementSTS(),
	)
	p.placeHA = helm.New(t,
		helm.WithValues("dapr_placement.ha=true"),
		helm.WithShowOnlyPlacementSTS(),
	)
	p.haInMem = helm.New(t,
		helm.WithGlobalValues("ha.enabled=true"),
		helm.WithValues("dapr_placement.cluster.forceInMemoryLog=true"),
		helm.WithShowOnlyPlacementSTS(),
	)
	p.nonHAInMem = helm.New(t,
		helm.WithValues("dapr_placement.cluster.forceInMemoryLog=true"),
		helm.WithShowOnlyPlacementSTS(),
	)

	return []framework.Option{
		framework.WithProcesses(p.nonHA, p.ha, p.placeHA, p.haInMem, p.nonHAInMem),
	}
}

func (p *raftLog) Run(t *testing.T, ctx context.Context) {
	sts := func(t *testing.T, h *helm.Helm) appsv1.StatefulSet {
		t.Helper()
		var sts appsv1.StatefulSet
		bs, err := io.ReadAll(h.Stdout(t))
		require.NoError(t, err)
		require.NoError(t, yaml.Unmarshal(bs, &sts))
		return sts
	}

	assertRaftLog := func(t *testing.T, sts appsv1.StatefulSet, want bool) {
		t.Helper()

		claims := make([]string, 0, len(sts.Spec.VolumeClaimTemplates))
		for _, c := range sts.Spec.VolumeClaimTemplates {
			claims = append(claims, c.Name)
		}

		require.Len(t, sts.Spec.Template.Spec.Containers, 1)
		container := sts.Spec.Template.Spec.Containers[0]
		var mounted bool
		for _, m := range container.VolumeMounts {
			if m.Name == "raft-log" {
				mounted = true
			}
		}

		if want {
			assert.Equal(t, []string{"raft-log"}, claims)
			assert.True(t, mounted, "volume mounts: %v", container.VolumeMounts)
			assert.Contains(t, container.Args, "--raft-logstore-path")
		} else {
			assert.Empty(t, claims)
			assert.False(t, mounted, "volume mounts: %v", container.VolumeMounts)
			assert.NotContains(t, container.Args, "--raft-logstore-path")
		}
	}

	t.Run("non-HA keeps the raft log in memory without a claim", func(t *testing.T) {
		assertRaftLog(t, sts(t, p.nonHA), false)
	})

	t.Run("global HA persists the raft log on a claim", func(t *testing.T) {
		assertRaftLog(t, sts(t, p.ha), true)
	})

	t.Run("placement HA persists the raft log on a claim", func(t *testing.T) {
		assertRaftLog(t, sts(t, p.placeHA), true)
	})

	t.Run("HA with forceInMemoryLog has no claim", func(t *testing.T) {
		assertRaftLog(t, sts(t, p.haInMem), false)
	})

	t.Run("non-HA with forceInMemoryLog has no claim", func(t *testing.T) {
		assertRaftLog(t, sts(t, p.nonHAInMem), false)
	})
}
