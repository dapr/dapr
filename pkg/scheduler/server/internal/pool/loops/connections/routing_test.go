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

package connections

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/connections/store"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/ptr"
)

func TestGetStreamLoopActorRouting(t *testing.T) {
	t.Parallel()

	newConns := func(placementEnabled bool) *connections {
		pool := store.New()
		for _, addr := range []string{"127.0.0.1:1", "127.0.0.1:2", "127.0.0.1:3"} {
			pool.Add(store.Options{
				ActorTypes:   []string{"mytype"},
				ActorAddress: ptr.Of(addr),
				Loop:         loop.New[loops.EventStream](1).NewLoop(nil),
			})
		}
		return &connections{streamPool: pool, placementEnabled: placementEnabled}
	}

	meta := &schedulerv1pb.JobMetadata{
		Target: &schedulerv1pb.JobTargetMetadata{
			Type: &schedulerv1pb.JobTargetMetadata_Actor{
				Actor: &schedulerv1pb.TargetActorReminder{Type: "mytype", Id: "myid"},
			},
		},
	}

	t.Run("an actor's triggers all route to its owner host", func(t *testing.T) {
		c := newConns(true)
		first, ok := c.getStreamLoop(meta)
		require.True(t, ok)
		for range 10 {
			l, lok := c.getStreamLoop(meta)
			require.True(t, lok)
			assert.Same(t, first, l)
		}
	})

	t.Run("without placement served here triggers round robin", func(t *testing.T) {
		c := newConns(false)
		seen := make(map[loop.Interface[loops.EventStream]]struct{})
		for range 9 {
			l, lok := c.getStreamLoop(meta)
			require.True(t, lok)
			seen[l] = struct{}{}
		}
		assert.Len(t, seen, 3)
	})
}
