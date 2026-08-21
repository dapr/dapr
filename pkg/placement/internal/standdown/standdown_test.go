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

package standdown

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	schedulerfake "github.com/dapr/dapr/pkg/scheduler/server/fake"
	"github.com/dapr/dapr/pkg/security/fake"
)

func TestInactiveByDefault(t *testing.T) {
	t.Parallel()
	s := New(Options{Security: fake.New()})
	assert.False(t, s.Active())
}

// TestNoAddressesNeverStandsDown asserts a placement service without
// scheduler addresses serves unconditionally: Run blocks until cancelled and
// never activates.
func TestNoAddressesNeverStandsDown(t *testing.T) {
	t.Parallel()

	var called bool
	s := New(Options{
		Security:    fake.New(),
		OnStandDown: func() { called = true },
	})

	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond*100)
	defer cancel()

	err := s.Run(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, s.Active())
	assert.False(t, called)
}

// TestUnreachableSchedulersKeepServing asserts unreachable schedulers fail
// open: the watcher retries rather than standing down, so a scheduler outage
// cannot take the placement service with it.
func TestUnreachableSchedulersKeepServing(t *testing.T) {
	t.Parallel()

	var called bool
	s := New(Options{
		Addresses:   []string{"127.0.0.1:1"},
		Security:    fake.New(),
		OnStandDown: func() { called = true },
	})

	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond*500)
	defer cancel()

	err := s.Run(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, s.Active())
	assert.False(t, called)
}

// TestHungSchedulerCompletesFirstObservation asserts a scheduler which
// accepts the connection but never answers does not block serving: the
// first observation completes on its timeout.
func TestHungSchedulerCompletesFirstObservation(t *testing.T) {
	t.Parallel()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { lis.Close() })
	go func() {
		for {
			conn, aerr := lis.Accept()
			if aerr != nil {
				return
			}
			// Hold the connection open without ever answering.
			t.Cleanup(func() { conn.Close() })
		}
	}()

	s := New(Options{
		Addresses: []string{lis.Addr().String()},
		Security:  fake.New(),
	})

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go s.Run(ctx)

	select {
	case <-s.FirstObservation():
	case <-time.After(firstObservationTimeout + time.Second*5):
		require.Fail(t, "a hung scheduler watch must not block the first observation")
	}
	assert.False(t, s.Active())
}

// TestRollbackStandsUp asserts a stand-down is revoked once the schedulers
// stop serving placement, as on a rollback.
func TestRollbackStandsUp(t *testing.T) {
	t.Parallel()

	hosts := make(chan []*schedulerv1pb.Host, 4)
	sched := schedulerfake.New(t).WithWatchHosts(func(_ *schedulerv1pb.WatchHostsRequest, stream schedulerv1pb.Scheduler_WatchHostsServer) error {
		for {
			select {
			case <-stream.Context().Done():
				return stream.Context().Err()
			case h := <-hosts:
				if err := stream.Send(&schedulerv1pb.WatchHostsResponse{Hosts: h}); err != nil {
					return err
				}
			}
		}
	})

	var downs, ups int
	s := New(Options{
		Addresses: []string{sched.Address()},
		Security: fake.New().WithGRPCDialOptionMTLSFn(func(spiffeid.ID) grpc.DialOption {
			return grpc.WithTransportCredentials(insecure.NewCredentials())
		}),
		OnStandDown: func() { downs++ },
		OnStandUp:   func() { ups++ },
	})

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go s.Run(ctx)

	// A scheduler advertises the placement leader: stand down.
	hosts <- []*schedulerv1pb.Host{{Address: "a:1", SchedulerPlacementEnabled: true, Leader: true}}
	require.Eventually(t, s.Active, time.Second*10, time.Millisecond*10)
	assert.Equal(t, 1, downs)

	// The schedulers rolled back, none serves placement: stand up.
	hosts <- []*schedulerv1pb.Host{{Address: "a:1"}}
	require.Eventually(t, func() bool { return !s.Active() }, time.Second*10, time.Millisecond*10)
	assert.Equal(t, 1, ups)

	// The cutover happens again: stand down again.
	hosts <- []*schedulerv1pb.Host{{Address: "a:1", SchedulerPlacementEnabled: true, Leader: true}}
	require.Eventually(t, s.Active, time.Second*10, time.Millisecond*10)
	assert.Equal(t, 2, downs)
}

// TestInheritedStandDownStandsUp asserts a stand-down inherited from the
// raft log is revoked once the schedulers stop serving placement.
func TestInheritedStandDownStandsUp(t *testing.T) {
	t.Parallel()

	hosts := make(chan []*schedulerv1pb.Host, 1)
	sched := schedulerfake.New(t).WithWatchHosts(func(_ *schedulerv1pb.WatchHostsRequest, stream schedulerv1pb.Scheduler_WatchHostsServer) error {
		for {
			select {
			case <-stream.Context().Done():
				return stream.Context().Err()
			case h := <-hosts:
				if err := stream.Send(&schedulerv1pb.WatchHostsResponse{Hosts: h}); err != nil {
					return err
				}
			}
		}
	})

	var ups int
	s := New(Options{
		Addresses: []string{sched.Address()},
		Security: fake.New().WithGRPCDialOptionMTLSFn(func(spiffeid.ID) grpc.DialOption {
			return grpc.WithTransportCredentials(insecure.NewCredentials())
		}),
		OnStandUp: func() { ups++ },
	})
	s.Inherit()
	require.True(t, s.Active())

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go s.Run(ctx)

	hosts <- []*schedulerv1pb.Host{{Address: "a:1"}}
	require.Eventually(t, func() bool { return !s.Active() }, time.Second*10, time.Millisecond*10)
	assert.Equal(t, 1, ups)
}
