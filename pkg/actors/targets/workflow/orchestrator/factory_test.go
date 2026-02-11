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

package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/internal/placement"
	placementfake "github.com/dapr/dapr/pkg/actors/internal/placement/fake"
	"github.com/dapr/durabletask-go/backend"
)

func Test_GetOrCreate(t *testing.T) {
	fact, err := New(t.Context(), Options{
		AppID:             "appID",
		ActivityActorType: "activity",
		WorkflowActorType: "workflow",
		Scheduler: func(context.Context, *backend.OrchestrationWorkItem) error {
			return nil
		},
		Actors: fake.New(),
	})
	require.NoError(t, err)

	ff := fact.(*factory)
	assert.Equal(t, 0, mapLen(ff))

	fact.GetOrCreate("foo1")
	fact.GetOrCreate("foo2")
	fact.GetOrCreate("foo3")
	fact.GetOrCreate("foo3")
	fact.GetOrCreate("foo3")

	assert.Equal(t, 3, mapLen(ff))
	assert.True(t, fact.Exists("foo1"))
	assert.True(t, fact.Exists("foo2"))
	assert.True(t, fact.Exists("foo3"))
	assert.False(t, fact.Exists("foo4"))

	act := fact.GetOrCreate("foo3")
	require.NoError(t, act.Deactivate(t.Context()))
	assert.Equal(t, 2, mapLen(ff))
	assert.False(t, fact.Exists("foo3"))
	fact.GetOrCreate("foo3")
	assert.Equal(t, 3, mapLen(ff))
	assert.True(t, fact.Exists("foo2"))

	act = fact.GetOrCreate("foo1")
	require.NoError(t, act.Deactivate(t.Context()))
	act = fact.GetOrCreate("foo2")
	require.NoError(t, act.Deactivate(t.Context()))
	act = fact.GetOrCreate("foo3")
	require.NoError(t, act.Deactivate(t.Context()))
	assert.Equal(t, 0, mapLen(ff))
}

func Test_HaltAll(t *testing.T) {
	fact, err := New(t.Context(), Options{
		AppID:             "appID",
		ActivityActorType: "activity",
		WorkflowActorType: "workflow",
		Scheduler: func(context.Context, *backend.OrchestrationWorkItem) error {
			return nil
		},
		Actors: fake.New(),
	})
	require.NoError(t, err)

	ff := fact.(*factory)
	assert.Equal(t, 0, mapLen(ff))

	fact.GetOrCreate("foo1")
	fact.GetOrCreate("foo2")
	fact.GetOrCreate("foo3")
	fact.GetOrCreate("foo4")
	fact.GetOrCreate("foo5")

	assert.Equal(t, 5, mapLen(ff))

	require.NoError(t, fact.HaltAll(t.Context()))
	assert.Equal(t, 0, mapLen(ff))
}

func Test_HaltNonHosted(t *testing.T) {
	hosted := map[string]bool{
		"foo1": true,
		"foo2": true,
		"foo5": true,
		"foo7": true,
	}

	pl := placementfake.New().WithIsActorHosted(func(_ context.Context, _, actorID string) bool {
		return hosted[actorID]
	})

	fact, err := New(t.Context(), Options{
		AppID:             "appID",
		ActivityActorType: "activity",
		WorkflowActorType: "workflow",
		Scheduler: func(context.Context, *backend.OrchestrationWorkItem) error {
			return nil
		},
		Actors: fake.New().WithPlacement(func(context.Context) (placement.Interface, error) {
			return pl, nil
		}),
	})
	require.NoError(t, err)

	ff := fact.(*factory)
	assert.Equal(t, 0, mapLen(ff))

	fact.GetOrCreate("foo1")
	fact.GetOrCreate("foo2")
	fact.GetOrCreate("foo3")
	fact.GetOrCreate("foo4")
	fact.GetOrCreate("foo5")
	fact.GetOrCreate("foo6")
	fact.GetOrCreate("foo7")

	assert.Equal(t, 7, mapLen(ff))

	require.NoError(t, fact.HaltNonHosted(t.Context(), func(r *api.LookupActorRequest) bool {
		return pl.IsActorHosted(t.Context(), r.ActorType, r.ActorID)
	}))
	assert.Equal(t, 4, mapLen(ff))
}

func mapLen(ff *factory) int {
	var i int
	ff.table.Range(func(any, any) bool {
		i++
		return true
	})
	return i
}
