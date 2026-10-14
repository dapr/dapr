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

package table

import (
	"context"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/fake"
)

func TestHaltAfterClaimCheckRemovesCreatedActor(t *testing.T) {
	t.Parallel()

	for name, halt := range map[string]func(*table) error{
		"non hosted": func(tbl *table) error {
			return tbl.HaltNonHosted(context.Background(), func(*api.LookupActorRequest) bool { return false })
		},
		"all": func(tbl *table) error {
			return tbl.HaltAll(context.Background())
		},
		"unregister": func(tbl *table) error {
			return tbl.UnRegisterActorTypes("test1")
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tbl, ok := New(Options{ReentrancyStore: reentrancystore.New()}).(*table)
			require.True(t, ok)
			claim, cancelClaim := context.WithCancel(t.Context())
			haltDone := make(chan error, 1)

			var actors sync.Map
			removeAll := func(context.Context) error {
				actors.Clear()
				return nil
			}
			factory := fake.NewFactory().
				WithGetOrCreate(func(actorID string) targets.Interface {
					cancelClaim()
					go func() { haltDone <- halt(tbl) }()
					waitHaltBlockedOrDone(t, tbl, "test1", haltDone)
					target := fake.New("test1")
					actors.Store(actorID, target)
					return target
				}).
				WithHaltAll(removeAll).
				WithHaltNonHosted(func(ctx context.Context, _ func(*api.LookupActorRequest) bool) error {
					return removeAll(ctx)
				})
			tbl.RegisterActorTypes(RegisterActorTypeOptions{
				Factories: []ActorTypeFactory{{Type: "test1", Factory: factory}},
			})

			_, err := tbl.GetOrCreate(claim, "test1", "a")
			require.NoError(t, err)
			require.NoError(t, <-haltDone)

			_, exists := actors.Load("a")
			assert.False(t, exists, "the halt left an actor created for a cancelled claim")

			_, err = tbl.GetOrCreate(claim, "test1", "a")
			require.ErrorIs(t, err, context.Canceled)
		})
	}
}

func waitHaltBlockedOrDone(t *testing.T, tbl *table, actorType string, haltDone chan error) {
	t.Helper()
	l, ok := tbl.createLocks.Load(actorType)
	require.True(t, ok)
	lock := l.(*sync.RWMutex)
	for {
		select {
		case err := <-haltDone:
			haltDone <- err
			return
		default:
		}
		if !lock.TryRLock() {
			return
		}
		lock.RUnlock()
		runtime.Gosched()
	}
}
