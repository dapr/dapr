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

	leaderconnector "github.com/dapr/dapr/pkg/actors/internal/placement/connector/leader"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator/inflight"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/stream/transport"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	healthzfake "github.com/dapr/dapr/pkg/healthz/fake"
	"github.com/dapr/dapr/pkg/runtime/scheduler/leadership"
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

// TestAdoptionRequiresAdvertisedLeader asserts an unreachable placement
// service moves the sidecar onto the scheduler only when a scheduler
// placement leader is advertised, and a FailedPrecondition close alone
// never swaps the authority.
func TestAdoptionRequiresAdvertisedLeader(t *testing.T) {
	t.Parallel()

	newPlacement := func(ldr *leadership.Leadership) *placement {
		ready := &atomic.Bool{}
		ready.Store(true)
		return &placement{
			id:         "test-id",
			namespace:  "default",
			ready:      ready,
			htarget:    healthzfake.New(),
			dissLoop:   loopfake.New[loops.EventDiss](),
			actorTable: tablefake.New(),
			inflight:   inflight.New(inflight.Options{Hostname: "localhost", Port: "3500"}),
			idx:        1,
			leadership: ldr,
			connector:  &fakeConnector{addr: "placement"},
			alt: &Fallback{
				Connector:          &fakeConnector{addr: "scheduler"},
				SchedulerPlacement: true,
			},
		}
	}

	t.Run("no advertised leader keeps the placement service", func(t *testing.T) {
		t.Parallel()
		p := newPlacement(leadership.New())
		ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond*50)
		t.Cleanup(cancel)
		err := p.handleCloseStream(ctx, &loops.ConnCloseStream{
			IDx:   1,
			Error: status.Error(codes.FailedPrecondition, "node is not a leader"),
		})
		require.Error(t, err)
		assert.Equal(t, "placement", p.connector.Address(),
			"leadership churn must not move the sidecar off its authority")
	})

	t.Run("advertised leader adopts the scheduler on connect failure", func(t *testing.T) {
		t.Parallel()
		ldr := leadership.New()
		ldr.Set("127.0.0.1:1")
		p := newPlacement(ldr)
		ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond*250)
		t.Cleanup(cancel)
		err := p.handleCloseStream(ctx, &loops.ConnCloseStream{
			IDx:   1,
			Error: errors.New("connection refused"),
		})
		require.Error(t, err)
		assert.Equal(t, "scheduler", p.connector.Address(),
			"an advertised scheduler placement leader is the handover signal")
	})
}

// TestStartupWaitAdoptsFallback covers a scheduler which never answers with
// a placement service configured: the bounded startup wait expires and the
// placement service is adopted, keeping the scheduler as the alternative.
func TestStartupWaitAdoptsFallback(t *testing.T) {
	t.Parallel()

	ready := &atomic.Bool{}
	p := &placement{
		id:          "test-id",
		namespace:   "default",
		ready:       ready,
		htarget:     healthzfake.New(),
		actorTable:  tablefake.New(),
		inflight:    inflight.New(inflight.Options{Hostname: "localhost", Port: "3500"}),
		startupWait: time.Millisecond * 100,
		connector:   leaderconnector.New(leaderconnector.Options{Leadership: leadership.New()}),
		streamFactory: func(context.Context, *grpc.ClientConn) (transport.Transport, error) {
			return nil, errors.New("no stream")
		},
		schedulerPlacement: true,
		fallback: &Fallback{
			Connector: &fakeConnector{addr: "placement"},
		},
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	t.Cleanup(cancel)
	err := p.handleReconnect(ctx, &loops.PlacementReconnect{})
	require.Error(t, err)
	assert.Nil(t, p.fallback)
	assert.Equal(t, "placement", p.connector.Address(),
		"the bounded wait must adopt the placement service")
	require.NotNil(t, p.alt)
	assert.True(t, p.alt.SchedulerPlacement,
		"the scheduler side must be kept as the alternative")
}
