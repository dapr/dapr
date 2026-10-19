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

	"github.com/dapr/durabletask-go/backend"
)

func TestHeldMarker(t *testing.T) {
	assert.False(t, wasHeld(t.Context()), "a ctx without a marker reports held")

	ctx := WithHeldMarker(t.Context())
	assert.False(t, wasHeld(ctx), "an unmarked ctx reports held")

	markHeld(ctx)
	assert.True(t, wasHeld(ctx))

	// Marking a ctx without a marker is a no-op.
	markHeld(t.Context())
}

// The executor waits after a start, terminate, suspend or resume. When the
// backend held the operation, the watch returns at once: nothing changes
// until a worker connects.
func TestWatchWorkflowRuntimeStatusSkipsHeld(t *testing.T) {
	ctx := WithHeldMarker(t.Context())
	markHeld(ctx)

	// A zero Actors has no actor runtime: reaching it would panic.
	abe := new(Actors)
	require.NoError(t, abe.WatchWorkflowRuntimeStatus(ctx, "id", nil, func(*backend.WorkflowMetadata) bool {
		t.Error("condition called for a held operation")
		return true
	}))
}
