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

package disseminator

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator/inflight"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator/timeout"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	healthzfake "github.com/dapr/dapr/pkg/healthz/fake"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	schedfake "github.com/dapr/dapr/pkg/runtime/scheduler/client/fake"
	loopfake "github.com/dapr/kit/events/loop/fake"
)

// newTestDisseminator creates a disseminator with minimal dependencies for testing
// the handleOrder method.
func newTestDisseminator(t *testing.T) (*disseminator, *healthzfake.Fake, *schedfake.Fake) {
	t.Helper()

	ht := healthzfake.New()
	sched := schedfake.New()

	inf := inflight.New(inflight.Options{
		Hostname: "localhost",
		Port:     "3500",
	})

	streamLoop := loopfake.New[loops.EventStream]()

	dissLoop := LoopFactoryCache.NewLoop(nil)

	diss := &disseminator{
		namespace:        "default",
		id:               "test-id",
		actorTable:       tablefake.New(),
		scheduler:        sched,
		healthTarget:     ht,
		inflight:         inf,
		streamLoop:       streamLoop,
		currentOperation: v1pb.HostOperation_LOCK,
		currentVersion:   0,
		timeout:          time.Second * 30,
		ready:            new(atomic.Bool),
	}

	diss.loop = dissLoop
	diss.timeoutQ = timeout.New(timeout.Options{
		Loop:    dissLoop,
		Timeout: time.Second * 30,
	})

	return diss, ht, sched
}

func TestHandleOrder_UnlockVersionMismatch(t *testing.T) {
	t.Run("unlock with lower version is ignored and currentVersion is preserved", func(t *testing.T) {
		diss, ht, _ := newTestDisseminator(t)

		// Simulate a LOCK at version 10.
		err := diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationLock,
				Version:   10,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, uint64(10), diss.currentVersion)

		// Simulate an UPDATE at version 10.
		err = diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationUpdate,
				Version:   10,
				Tables:    &v1pb.PlacementTables{},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, v1pb.HostOperation_UPDATE, diss.currentOperation)

		// Attempt UNLOCK with version 5 (lower than currentVersion=10).
		// This should be ignored and currentVersion should remain 10.
		err = diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationUnlock,
				Version:   5,
			},
		})
		require.NoError(t, err)

		assert.Equal(t, uint64(10), diss.currentVersion,
			"currentVersion should not be updated when unlock version is lower")
		assert.Equal(t, v1pb.HostOperation_UPDATE, diss.currentOperation,
			"operation should remain UPDATE when unlock is ignored")
		assert.False(t, ht.ReadyCalled(),
			"health target should not be marked ready when unlock is ignored")
	})

	t.Run("unlock with matching version succeeds and updates currentVersion", func(t *testing.T) {
		diss, ht, _ := newTestDisseminator(t)

		// LOCK -> UPDATE -> UNLOCK at version 10.
		err := diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationLock,
				Version:   10,
			},
		})
		require.NoError(t, err)

		err = diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationUpdate,
				Version:   10,
				Tables:    &v1pb.PlacementTables{},
			},
		})
		require.NoError(t, err)

		err = diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationUnlock,
				Version:   10,
			},
		})
		require.NoError(t, err)

		assert.Equal(t, uint64(10), diss.currentVersion,
			"currentVersion should be set to matching unlock version")
		assert.Equal(t, v1pb.HostOperation_UNLOCK, diss.currentOperation)
		assert.True(t, ht.ReadyCalled(),
			"health target should be marked ready on successful unlock")
	})

	t.Run("unlock with higher version succeeds and advances currentVersion", func(t *testing.T) {
		diss, ht, _ := newTestDisseminator(t)

		// LOCK -> UPDATE at version 5, UNLOCK at version 10.
		err := diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationLock,
				Version:   5,
			},
		})
		require.NoError(t, err)

		err = diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationUpdate,
				Version:   5,
				Tables:    &v1pb.PlacementTables{},
			},
		})
		require.NoError(t, err)

		err = diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationUnlock,
				Version:   10,
			},
		})
		require.NoError(t, err)

		assert.Equal(t, uint64(10), diss.currentVersion,
			"currentVersion should advance to higher unlock version")
		assert.Equal(t, v1pb.HostOperation_UNLOCK, diss.currentOperation)
		assert.True(t, ht.ReadyCalled())
	})
}
