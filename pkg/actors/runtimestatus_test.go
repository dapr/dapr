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

package actors

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	inmemory "github.com/dapr/components-contrib/state/in-memory"
	"github.com/dapr/dapr/pkg/healthz"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/kit/logger"
)

// TestRuntimeStatusBeforeRegistration covers the window between process
// start and host registration completing: DISABLED means no placement
// authority is configured at all, INITIALIZING means one is — including a
// scheduler serving placement with no placement addresses configured.
func TestRuntimeStatusBeforeRegistration(t *testing.T) {
	t.Parallel()

	newActors := func(t *testing.T, placementAddresses []string, schedulerPlacement bool) *actors {
		t.Helper()
		a, ok := New(Options{
			AppID:                     "app-1",
			Namespace:                 "ns-1",
			PlacementAddresses:        placementAddresses,
			SchedulerPlacementEnabled: schedulerPlacement,
			CompStore:                 compstore.New(),
			Healthz:                   healthz.New(),
		}).(*actors)
		require.True(t, ok)
		return a
	}

	tests := map[string]struct {
		placementAddresses []string
		schedulerPlacement bool
		expStatus          runtimev1pb.ActorRuntime_ActorRuntimeStatus
	}{
		"placement service configured is initializing": {
			placementAddresses: []string{"10.0.0.1:50005"},
			schedulerPlacement: false,
			expStatus:          runtimev1pb.ActorRuntime_INITIALIZING,
		},
		"scheduler placement with no placement address is initializing": {
			placementAddresses: nil,
			schedulerPlacement: true,
			expStatus:          runtimev1pb.ActorRuntime_INITIALIZING,
		},
		"scheduler placement with a blank placement address is initializing": {
			placementAddresses: []string{""},
			schedulerPlacement: true,
			expStatus:          runtimev1pb.ActorRuntime_INITIALIZING,
		},
		"both configured is initializing": {
			placementAddresses: []string{"10.0.0.1:50005"},
			schedulerPlacement: true,
			expStatus:          runtimev1pb.ActorRuntime_INITIALIZING,
		},
		"no authority at all is disabled": {
			placementAddresses: nil,
			schedulerPlacement: false,
			expStatus:          runtimev1pb.ActorRuntime_DISABLED,
		},
		"blank placement address and no scheduler is disabled": {
			placementAddresses: []string{""},
			schedulerPlacement: false,
			expStatus:          runtimev1pb.ActorRuntime_DISABLED,
		},
		"quoted blank placement address and no scheduler is disabled": {
			placementAddresses: []string{`"   "`},
			schedulerPlacement: false,
			expStatus:          runtimev1pb.ActorRuntime_DISABLED,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			status := newActors(t, test.placementAddresses, test.schedulerPlacement).RuntimeStatus()
			assert.Equal(t, test.expStatus, status.GetRuntimeStatus())

			if test.expStatus == runtimev1pb.ActorRuntime_DISABLED {
				assert.Equal(t, "placement: disconnected", status.GetPlacement())
			}
		})
	}
}

// TestRuntimeStatusDisabledAfterRegistration asserts the post-registration
// branch still reports DISABLED when no placement authority was configured.
func TestRuntimeStatusDisabledAfterRegistration(t *testing.T) {
	t.Parallel()

	a, ok := New(Options{
		AppID:     "app-1",
		Namespace: "ns-1",
		CompStore: compstore.New(),
		Healthz:   healthz.New(),
	}).(*actors)
	require.True(t, ok)

	close(a.registerDoneCh)

	status := a.RuntimeStatus()
	assert.Equal(t, runtimev1pb.ActorRuntime_DISABLED, status.GetRuntimeStatus())
	assert.Equal(t, "placement: disconnected", status.GetPlacement())
}

// TestRuntimeStatusStateStores asserts that the runtime status reports the
// state stores bound to actor types, sorted, with their actor types sorted,
// and whether each store is loaded at the time of the call.
func TestRuntimeStatusStateStores(t *testing.T) {
	t.Parallel()

	newActors := func(t *testing.T, cs *compstore.ComponentStore, placementAddresses []string, bindings map[string]string) *actors {
		t.Helper()
		a, ok := New(Options{
			AppID:              "app-1",
			Namespace:          "ns-1",
			PlacementAddresses: placementAddresses,
			CompStore:          cs,
			Healthz:            healthz.New(),
			ActorStateStores:   bindings,
		}).(*actors)
		require.True(t, ok)
		return a
	}

	bindings := map[string]string{
		"TransactionActor": "wallet-transactions",
		"AuditActor":       "wallet-transactions",
		"BalanceActor":     "wallet-balance",
	}

	t.Run("bound stores are reported with whether they are loaded", func(t *testing.T) {
		t.Parallel()

		cs := compstore.New()
		cs.AddStateStore("wallet-transactions", inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name())))
		a := newActors(t, cs, []string{"10.0.0.1:50005"}, bindings)

		status := a.RuntimeStatus()
		assert.Equal(t, runtimev1pb.ActorRuntime_INITIALIZING, status.GetRuntimeStatus())
		require.Len(t, status.GetStateStores(), 2)
		assert.Equal(t, "wallet-balance", status.GetStateStores()[0].GetName())
		assert.Equal(t, []string{"BalanceActor"}, status.GetStateStores()[0].GetActorTypes())
		assert.False(t, status.GetStateStores()[0].GetLoaded())
		assert.Equal(t, "wallet-transactions", status.GetStateStores()[1].GetName())
		assert.Equal(t, []string{"AuditActor", "TransactionActor"}, status.GetStateStores()[1].GetActorTypes())
		assert.True(t, status.GetStateStores()[1].GetLoaded())

		// A store loaded later is reported as loaded, and a removed one is not.
		cs.AddStateStore("wallet-balance", inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name())))
		cs.DeleteStateStore("wallet-transactions")
		status = a.RuntimeStatus()
		require.Len(t, status.GetStateStores(), 2)
		assert.True(t, status.GetStateStores()[0].GetLoaded())
		assert.False(t, status.GetStateStores()[1].GetLoaded())
	})

	t.Run("no bindings reports no state stores", func(t *testing.T) {
		t.Parallel()

		a := newActors(t, compstore.New(), []string{"10.0.0.1:50005"}, nil)
		assert.Empty(t, a.RuntimeStatus().GetStateStores())
	})

	t.Run("a disabled actor runtime reports no state stores", func(t *testing.T) {
		t.Parallel()

		a := newActors(t, compstore.New(), nil, bindings)
		status := a.RuntimeStatus()
		assert.Equal(t, runtimev1pb.ActorRuntime_DISABLED, status.GetRuntimeStatus())
		assert.Empty(t, status.GetStateStores())
	})
}
