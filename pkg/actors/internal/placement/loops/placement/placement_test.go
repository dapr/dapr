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

package placement

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	healthzfake "github.com/dapr/dapr/pkg/healthz/fake"
	loopfake "github.com/dapr/kit/events/loop/fake"
)

func TestHandleCloseStream_NotReady(t *testing.T) {
	t.Run("handleCloseStream sets ready to false and closes diss loop", func(t *testing.T) {
		ht := healthzfake.New()

		ready := &atomic.Bool{}
		ready.Store(true)

		ctx, cancel := context.WithCancel(t.Context())
		// Cancel context immediately so handleReconnect exits quickly.
		cancel()

		var dissLoopClosed atomic.Bool
		dissLoop := loopfake.New[loops.EventDiss]().
			WithClose(func(loops.EventDiss) {
				dissLoopClosed.Store(true)
			})

		p := &placement{
			id:         "test-id",
			namespace:  "default",
			ready:      ready,
			htarget:    ht,
			dissLoop:   dissLoop,
			actorTable: tablefake.New(),
			idx:        1,
		}

		err := p.handleCloseStream(ctx, &loops.ConnCloseStream{
			Error: errors.New("connection lost"),
			IDx:   1,
		})

		// Should return context.Canceled since we cancelled the context.
		require.Error(t, err)
		assert.Equal(t, context.Canceled, err)

		assert.False(t, ready.Load(),
			"ready flag should be false when stream closes")
		assert.True(t, dissLoopClosed.Load(),
			"dissemination loop should be closed")
	})

	t.Run("handleCloseStream with mismatched idx is ignored", func(t *testing.T) {
		ht := healthzfake.New()

		ready := &atomic.Bool{}
		ready.Store(true)

		p := &placement{
			id:        "test-id",
			namespace: "default",
			ready:     ready,
			htarget:   ht,
			idx:       2,
		}

		err := p.handleCloseStream(t.Context(), &loops.ConnCloseStream{
			Error: errors.New("connection lost"),
			IDx:   1, // Mismatched idx.
		})
		require.NoError(t, err)

		assert.True(t, ready.Load(),
			"ready flag should remain true when close stream idx doesn't match")
	})
}
