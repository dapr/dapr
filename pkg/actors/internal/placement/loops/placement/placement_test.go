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
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator/inflight"
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
			inflight:   inflight.New(inflight.Options{Hostname: "localhost", Port: "3500"}),
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

type fakeConnector struct{ addr string }

func (f *fakeConnector) Connect(context.Context) (*grpc.ClientConn, error) {
	return nil, errors.New("fake connector does not dial")
}
func (f *fakeConnector) Address() string { return f.addr }

func TestSwapAlt(t *testing.T) {
	t.Parallel()

	sched := &fakeConnector{addr: "scheduler"}
	place := &fakeConnector{addr: "placement"}
	p := &placement{
		connector:          place,
		schedulerPlacement: false,
		alt: &Fallback{
			Connector:          sched,
			SchedulerPlacement: true,
		},
	}

	p.swapAlt()
	assert.Equal(t, "scheduler", p.connector.Address())
	assert.True(t, p.schedulerPlacement)
	assert.Equal(t, "placement", p.alt.Connector.Address())
	assert.False(t, p.alt.SchedulerPlacement)

	p.swapAlt()
	assert.Equal(t, "placement", p.connector.Address())
	assert.False(t, p.schedulerPlacement)
	assert.Equal(t, "scheduler", p.alt.Connector.Address())
	assert.True(t, p.alt.SchedulerPlacement)
}

// TestHandleCloseStreamRefusalProbesAlt asserts a FailedPrecondition close,
// how a stood-down authority refuses, swaps to the kept alternative before
// reconnecting.
func TestHandleCloseStreamRefusalProbesAlt(t *testing.T) {
	t.Parallel()

	ready := &atomic.Bool{}
	ready.Store(true)
	p := &placement{
		id:         "test-id",
		namespace:  "default",
		ready:      ready,
		htarget:    healthzfake.New(),
		dissLoop:   loopfake.New[loops.EventDiss](),
		actorTable: tablefake.New(),
		inflight:   inflight.New(inflight.Options{Hostname: "localhost", Port: "3500"}),
		idx:        1,
		connector:  &fakeConnector{addr: "placement"},
		alt: &Fallback{
			Connector:          &fakeConnector{addr: "scheduler"},
			SchedulerPlacement: true,
		},
	}

	// The context outlives the close handling just long enough for the swap,
	// then ends the reconnect loop.
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond*50)
	t.Cleanup(cancel)
	err := p.handleCloseStream(ctx, &loops.ConnCloseStream{
		IDx:   1,
		Error: status.Error(codes.FailedPrecondition, "standing down"),
	})
	require.Error(t, err)
	assert.Equal(t, "scheduler", p.connector.Address(),
		"a refused stream must probe the other authority next")

	// A non-refusal close keeps the active connector.
	p.idx = 2
	p.dissLoop = loopfake.New[loops.EventDiss]()
	ctx2, cancel2 := context.WithTimeout(t.Context(), time.Millisecond*50)
	t.Cleanup(cancel2)
	err = p.handleCloseStream(ctx2, &loops.ConnCloseStream{
		IDx:   2,
		Error: errors.New("connection reset"),
	})
	require.Error(t, err)
	assert.Equal(t, "scheduler", p.connector.Address())
}
