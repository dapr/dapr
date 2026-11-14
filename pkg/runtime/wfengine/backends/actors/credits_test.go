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

	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func TestActivityCredits(t *testing.T) {
	t.Parallel()
	abe := &Actors{activityCap: 2}

	free, changed := abe.ActivityFreeSlots()
	assert.Equal(t, uint32(2), free)

	require.True(t, abe.reserveActivity(true))
	select {
	case <-changed:
	default:
		require.Fail(t, "a reservation must signal a change")
	}

	require.True(t, abe.reserveActivity(false))
	assert.False(t, abe.reserveActivity(true), "a pull delivery is refused at the cap")
	require.True(t, abe.reserveActivity(false), "other deliveries are counted and queue as before")
	free, _ = abe.ActivityFreeSlots()
	assert.Equal(t, uint32(0), free, "free never goes below zero")

	wi := func() *backend.ActivityWorkItem {
		return &backend.ActivityWorkItem{
			NewEvent:   &protos.HistoryEvent{},
			Properties: map[string]any{todo.CallbackChannelProperty: make(chan bool, 1)},
		}
	}
	require.NoError(t, abe.CompleteActivityWorkItem(t.Context(), wi()))
	require.NoError(t, abe.AbandonActivityWorkItem(t.Context(), wi()))
	free, _ = abe.ActivityFreeSlots()
	assert.Equal(t, uint32(1), free, "completed and abandoned work items free their slots")

	abe.releaseActivity()
	free, _ = abe.ActivityFreeSlots()
	assert.Equal(t, uint32(2), free)

	uncapped := &Actors{}
	assert.True(t, uncapped.reserveActivity(true), "without a cap nothing is refused")
}
