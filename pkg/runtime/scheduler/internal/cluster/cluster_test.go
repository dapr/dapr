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

package cluster

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/config"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	wfenginefake "github.com/dapr/dapr/pkg/runtime/wfengine/fake"
)

func Test_buildConcurrencyLimits_streamSlots(t *testing.T) {
	t.Parallel()

	name := "Transcode"
	streamLimits := func(limits []*schedulerv1pb.ConcurrencyLimit) []*schedulerv1pb.ConcurrencyLimit {
		var out []*schedulerv1pb.ConcurrencyLimit
		for _, l := range limits {
			if l.GetStream() != nil {
				out = append(out, l)
			}
		}
		return out
	}

	t.Run("no pull dispatch declares no stream slots", func(t *testing.T) {
		c := &Cluster{wfengine: wfenginefake.New(), workflowSpec: &config.WorkflowSpec{
			MaxConcurrentActivityInvocations: 4,
			ActivityConcurrencyLimits:        []config.ActivityConcurrencyLimit{{Name: &name, MaxConcurrent: new(int32(5))}},
		}}
		limits := c.buildConcurrencyLimits()
		require.Len(t, limits, 1)
		assert.Empty(t, streamLimits(limits))
	})

	t.Run("app-wide pull declares the per-sidecar cap as stream slots", func(t *testing.T) {
		c := &Cluster{wfengine: wfenginefake.New(), workflowSpec: &config.WorkflowSpec{
			MaxConcurrentActivityInvocations: 4,
			ActivityDispatchMode:             config.ActivityDispatchModePull,
		}}
		limits := streamLimits(c.buildConcurrencyLimits())
		require.Len(t, limits, 1)
		assert.Equal(t, int32(4), limits[0].GetMaxConcurrent())
		assert.Nil(t, limits[0].Name)
	})

	t.Run("a per-name pull override declares stream slots and keeps the global gate", func(t *testing.T) {
		c := &Cluster{wfengine: wfenginefake.New(), workflowSpec: &config.WorkflowSpec{
			MaxConcurrentActivityInvocations: 2,
			ActivityConcurrencyLimits: []config.ActivityConcurrencyLimit{{
				Name: &name, MaxConcurrent: new(int32(5)), DispatchMode: config.ActivityDispatchModePull,
			}},
		}}
		limits := c.buildConcurrencyLimits()
		require.Len(t, limits, 2)
		assert.Equal(t, int32(5), limits[0].GetMaxConcurrent())
		assert.NotNil(t, limits[0].GetActor())
		assert.Equal(t, name, limits[0].GetName())
		require.Len(t, streamLimits(limits), 1)
		assert.Equal(t, int32(2), streamLimits(limits)[0].GetMaxConcurrent())
	})

	t.Run("pull without a per-sidecar cap declares nothing", func(t *testing.T) {
		// Rejected by config validation; the builder must still not emit an
		// unbounded stream limit if it ever sees such a spec.
		c := &Cluster{wfengine: wfenginefake.New(), workflowSpec: &config.WorkflowSpec{
			ActivityDispatchMode: config.ActivityDispatchModePull,
		}}
		assert.Empty(t, streamLimits(c.buildConcurrencyLimits()))
	})
}
