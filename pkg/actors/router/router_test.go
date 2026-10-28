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

package router_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	placementfake "github.com/dapr/dapr/pkg/actors/internal/placement/fake"
	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	"github.com/dapr/dapr/pkg/actors/router"
	"github.com/dapr/dapr/pkg/actors/table"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	"github.com/dapr/dapr/pkg/actors/targets"
	targetsfake "github.com/dapr/dapr/pkg/actors/targets/fake"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/modes"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	securityfake "github.com/dapr/dapr/pkg/security/fake"
	"github.com/dapr/kit/logger"
)

type localTimerRouter struct {
	router.Interface
	lookups, releases, creates, invokes int
}

func newLocalTimerRouter(t *testing.T, lookup func(ctx context.Context, n int) (context.Context, error), invoke func(context.Context, *api.Reminder)) *localTimerRouter {
	t.Helper()
	l := new(localTimerRouter)
	plc := placementfake.New().WithLookupActor(func(ctx context.Context, req *api.LookupActorRequest) (*api.LookupActorResponse, context.Context, context.CancelCauseFunc, error) {
		l.lookups++
		if lookup != nil {
			var err error
			ctx, err = lookup(ctx, l.lookups)
			if err != nil {
				return nil, nil, nil, err
			}
		}
		return &api.LookupActorResponse{Local: true}, ctx, func(error) { l.releases++ }, nil
	})
	fn := func(ctx context.Context, req *api.Reminder) error {
		l.invokes++
		if invoke != nil {
			invoke(ctx, req)
		}
		return nil
	}
	target := targetsfake.New("abc").(*targetsfake.Fake).WithInvokeTimer(fn).WithInvokeReminder(fn).
		WithInvokeMethod(func(context.Context, *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
			l.invokes++
			return new(internalv1pb.InternalInvokeResponse), nil
		}).
		WithInvokeStream(func(context.Context, *internalv1pb.InternalInvokeRequest, func(*internalv1pb.InternalInvokeResponse) (bool, error)) error {
			l.invokes++
			return nil
		})
	tbl := table.New(table.Options{ReentrancyStore: reentrancystore.New()})
	t.Cleanup(func() { require.NoError(t, tbl.Close()) })
	tbl.RegisterActorTypes(table.RegisterActorTypeOptions{
		Factories: []table.ActorTypeFactory{{
			Type: "abc",
			Factory: targetsfake.NewFactory().WithGetOrCreate(func(string) targets.Interface {
				l.creates++
				return target
			}),
		}},
	})
	l.Interface = router.New(router.Options{
		Placement:  plc,
		Table:      tbl,
		Resiliency: resiliency.FromConfigurations(logger.NewLogger("test")),
	})
	return l
}

func removableTimer() (*api.Reminder, context.CancelFunc) {
	removed, remove := context.WithCancel(context.Background())
	return &api.Reminder{ActorType: "abc", ActorID: "foo", Name: "tick", IsTimer: true, Removed: removed}, remove
}

func TestCallReminderTimerRemovedDuringLookup(t *testing.T) {
	t.Parallel()

	for name, claimed := range map[string]bool{
		"lookup fails on the cancelled context": false,
		"lookup returns a claim":                true,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			waiting := make(chan struct{})
			r := newLocalTimerRouter(t, func(ctx context.Context, _ int) (context.Context, error) {
				close(waiting)
				<-ctx.Done()
				if claimed {
					return context.Background(), nil
				}
				return nil, ctx.Err()
			}, nil)

			timer, remove := removableTimer()
			errCh := make(chan error, 1)
			go func() { errCh <- r.CallReminder(t.Context(), timer) }()
			<-waiting
			remove()

			require.ErrorIs(t, <-errCh, actorerrors.ErrTimerFireStale)
			assert.Equal(t, 1, r.lookups, "a removed timer fire was retried")
			if claimed {
				assert.Equal(t, 1, r.releases, "the placement claim was not released")
			}
			assert.Equal(t, 0, r.creates)
			assert.Equal(t, 0, r.invokes)
		})
	}
}

func TestCallReminderTimerRemovedDuringRetryBackoff(t *testing.T) {
	t.Parallel()

	timer, remove := removableTimer()
	r := newLocalTimerRouter(t, func(ctx context.Context, n int) (context.Context, error) {
		if n == 1 {
			// The built-in retry backs off for 1s, so this lands in the backoff.
			time.AfterFunc(100*time.Millisecond, remove)
			return nil, errors.New("actor not found")
		}
		return ctx, nil
	}, nil)

	require.ErrorIs(t, r.CallReminder(t.Context(), timer), actorerrors.ErrTimerFireStale)
	assert.Equal(t, 2, r.lookups)
	assert.Equal(t, 0, r.creates, "a target was created for a removed timer")
	assert.Equal(t, 0, r.invokes)
}

func TestCallReminderTimerRemovedDuringInvokeCompletes(t *testing.T) {
	t.Parallel()

	timer, remove := removableTimer()
	var alive bool
	r := newLocalTimerRouter(t, nil, func(ctx context.Context, _ *api.Reminder) {
		remove()
		alive = ctx.Err() == nil
	})

	require.NoError(t, r.CallReminder(t.Context(), timer))
	assert.Equal(t, 1, r.invokes)
	assert.True(t, alive, "removing the timer cancelled its running callback")
}

func TestCallReminderCancelledClaimDoesNotCreateActor(t *testing.T) {
	t.Parallel()

	for name, isTimer := range map[string]bool{
		"timer":    true,
		"reminder": false,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newLocalTimerRouter(t, func(ctx context.Context, _ int) (context.Context, error) {
				ctx, cancel := context.WithCancel(ctx)
				cancel()
				return ctx, nil
			}, nil)

			err := r.CallReminder(t.Context(), &api.Reminder{ActorType: "abc", ActorID: "foo", Name: "tick", IsTimer: isTimer, SkipRetries: true})
			require.ErrorIs(t, err, context.Canceled)
			assert.Equal(t, 1, r.releases)
			assert.Equal(t, 0, r.creates, "a target was created for an actor whose claim was cancelled")
			assert.Equal(t, 0, r.invokes)
		})
	}
}

func TestCallReminderCancelledClaimRetried(t *testing.T) {
	t.Parallel()

	r := newLocalTimerRouter(t, cancelledClaimOnFirstLookup, nil)

	err := r.CallReminder(t.Context(), &api.Reminder{ActorType: "abc", ActorID: "foo", Name: "tick", IsTimer: true})
	require.NoError(t, err)
	assert.Equal(t, 2, r.lookups)
	assert.Equal(t, 1, r.creates)
	assert.Equal(t, 1, r.invokes, "a fire whose claim was cancelled was not retried")
}

func cancelledClaimOnFirstLookup(ctx context.Context, n int) (context.Context, error) {
	if n > 1 {
		return ctx, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	return ctx, nil
}

func TestCallCancelledClaimDoesNotCreateActor(t *testing.T) {
	t.Parallel()

	for name, call := range map[string]func(context.Context, router.Interface, *internalv1pb.InternalInvokeRequest) error{
		"method": func(ctx context.Context, r router.Interface, req *internalv1pb.InternalInvokeRequest) error {
			_, err := r.Call(ctx, req)
			return err
		},
		"stream": func(ctx context.Context, r router.Interface, req *internalv1pb.InternalInvokeRequest) error {
			return r.CallStream(ctx, req, func(*internalv1pb.InternalInvokeResponse) (bool, error) { return true, nil })
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newLocalTimerRouter(t, cancelledClaimOnFirstLookup, nil)
			require.NoError(t, call(t.Context(), r, internalv1pb.NewInternalInvokeRequest("method").WithActor("abc", "foo")))
			assert.Equal(t, 2, r.lookups, "a call whose claim was cancelled was not retried")
			assert.Equal(t, 1, r.creates, "a target was created for an actor whose claim was cancelled")
			assert.Equal(t, 1, r.invokes)
		})
	}
}

// TestCallWithoutRetriesReturnsLookupError covers the handoff's use of the
// router: with no address for the actor, the call makes one lookup and returns
// its error, instead of retrying it as Call does.
func TestCallWithoutRetriesReturnsLookupError(t *testing.T) {
	t.Parallel()

	r := newLocalTimerRouter(t, func(context.Context, int) (context.Context, error) {
		return nil, messages.ErrActorNoAddress.WithFormat("abc||foo")
	}, nil)
	_, err := r.CallWithoutRetries(t.Context(), internalv1pb.NewInternalInvokeRequest("method").WithActor("abc", "foo"))
	require.ErrorIs(t, err, messages.ErrActorNoAddress)
	assert.Equal(t, 1, r.lookups, "the call retried its lookup")
	assert.Equal(t, 0, r.creates)
	assert.Equal(t, 0, r.invokes)
}

func TestCallReminderNonLocalTimerDropped(t *testing.T) {
	var lookups int
	plc := placementfake.New().WithLookupActor(func(ctx context.Context, req *api.LookupActorRequest) (*api.LookupActorResponse, context.Context, context.CancelCauseFunc, error) {
		lookups++
		return &api.LookupActorResponse{Local: false, Address: "remote:1234", AppID: "other"}, ctx, func(error) {}, nil
	})

	r := router.New(router.Options{
		Placement:  plc,
		Table:      tablefake.New(),
		Resiliency: resiliency.New(logger.NewLogger("test")),
	})

	// The nil GRPC manager panics if the remote-forward path is reached.
	err := r.CallReminder(t.Context(), &api.Reminder{
		ActorType: "abc",
		ActorID:   "foo",
		Name:      "tick",
		IsTimer:   true,
	})
	require.ErrorIs(t, err, actorerrors.ErrTimerFireNotLocal)
	assert.Equal(t, 1, lookups)
}

func TestCallReminderNonLocalRemoteReminderErrors(t *testing.T) {
	plc := placementfake.New().WithLookupActor(func(ctx context.Context, req *api.LookupActorRequest) (*api.LookupActorResponse, context.Context, context.CancelCauseFunc, error) {
		return &api.LookupActorResponse{Local: false, Address: "remote:1234", AppID: "other"}, ctx, func(error) {}, nil
	})

	r := router.New(router.Options{
		Placement:  plc,
		Table:      tablefake.New(),
		Resiliency: resiliency.New(logger.NewLogger("test")),
	})

	err := r.CallReminder(t.Context(), &api.Reminder{
		ActorType: "abc",
		ActorID:   "foo",
		Name:      "remind",
		IsRemote:  true,
	})
	require.ErrorContains(t, err, "remote actor moved")
}

// A fired reminder forwarded to the actor's owner carries the creator the
// Scheduler verified, so the owner can judge it as if delivered directly.
func TestCallReminderRemoteForwardsSourceAppID(t *testing.T) {
	received := make(chan *internalv1pb.Reminder, 1)
	srv := grpc.NewServer()
	internalv1pb.RegisterServiceInvocationServer(srv, &reminderSink{received: received})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)

	plc := placementfake.New().WithLookupActor(func(ctx context.Context, req *api.LookupActorRequest) (*api.LookupActorResponse, context.Context, context.CancelCauseFunc, error) {
		return &api.LookupActorResponse{Local: false, Address: lis.Addr().String(), AppID: "target"}, ctx, func(error) {}, nil
	})

	r := router.New(router.Options{
		Namespace:  "default",
		AppID:      "target",
		Placement:  plc,
		Table:      tablefake.New(),
		Resiliency: resiliency.New(logger.NewLogger("test")),
		GRPC:       manager.NewManager(securityfake.New(), modes.StandaloneMode, &manager.AppChannelConfig{}),
	})

	recv := func(t *testing.T) *internalv1pb.Reminder {
		t.Helper()
		select {
		case got := <-received:
			return got
		case <-time.After(time.Second * 5):
			require.Fail(t, "the owner did not receive the forwarded reminder")
			return nil
		}
	}

	require.NoError(t, r.CallReminder(t.Context(), &api.Reminder{
		ActorType:   "dapr.internal.default.target.workflow",
		ActorID:     "instance-1",
		Name:        "activity-result-abc",
		SourceAppID: "creator",
	}))
	got := recv(t)
	require.NotNil(t, got.SourceAppId)
	assert.Equal(t, "creator", got.GetSourceAppId())
	assert.Equal(t, "activity-result-abc", got.GetName())
	assert.Equal(t, "instance-1", got.GetActorId())

	require.NoError(t, r.CallReminder(t.Context(), &api.Reminder{
		ActorType: "dapr.internal.default.target.workflow",
		ActorID:   "instance-1",
		Name:      "activity-result-unknown",
	}))
	assert.Nil(t, recv(t).SourceAppId, "an unknown creator is unset, not empty")
}

type reminderSink struct {
	internalv1pb.UnimplementedServiceInvocationServer
	received chan *internalv1pb.Reminder
}

func (s *reminderSink) CallActorReminder(_ context.Context, in *internalv1pb.Reminder) (*emptypb.Empty, error) {
	s.received <- in
	return new(emptypb.Empty), nil
}

func TestIsRetryableRemoteError(t *testing.T) {
	tests := map[string]struct {
		err  error
		want bool
	}{
		"unavailable":               {err: status.Error(codes.Unavailable, "connection refused"), want: true},
		"actor moved":               {err: status.Error(codes.Internal, "error invoke actor method: remote actor moved"), want: true},
		"placement disseminating":   {err: status.Error(codes.Internal, "lookup: placement is disseminating"), want: true},
		"other internal":            {err: status.Error(codes.Internal, "boom"), want: false},
		"aborted is for the caller": {err: status.Error(codes.Aborted, "retry later"), want: false},
		"already exists":            {err: status.Error(codes.AlreadyExists, "taken"), want: false},
		"not a status":              {err: errors.New("plain"), want: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, router.IsRetryableRemoteError(tc.err))
		})
	}
}
