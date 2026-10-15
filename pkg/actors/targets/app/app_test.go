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

package app

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	placementfake "github.com/dapr/dapr/pkg/actors/internal/placement/fake"
	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/app/transport"
	targetserrors "github.com/dapr/dapr/pkg/actors/targets/errors"
	"github.com/dapr/dapr/pkg/channel/fake"
	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
)

type callback struct {
	call string
	ids  []string
}

type callLog struct {
	lock  sync.Mutex
	calls []callback
}

func (c *callLog) peek() []callback {
	c.lock.Lock()
	defer c.lock.Unlock()
	return slices.Clone(c.calls)
}

func (c *callLog) take() []callback {
	c.lock.Lock()
	defer c.lock.Unlock()
	calls := c.calls
	c.calls = nil
	return calls
}

func newActivationFactory(t *testing.T, opts ...func(*Options)) (targets.Factory, *callLog) {
	t.Helper()

	calls := new(callLog)
	o := Options{
		ActorType:   "abc",
		Reentrancy:  reentrancystore.New(),
		Resiliency:  resiliency.New(logger.NewLogger("test")),
		Placement:   placementfake.New(),
		IdleTimeout: time.Minute,
		AppChannel: fake.New().WithInvokeMethod(func(_ context.Context, req *invokev1.InvokeMethodRequest, _ string) (*invokev1.InvokeMethodResponse, error) {
			calls.lock.Lock()
			calls.calls = append(calls.calls, callback{
				call: req.Message().GetHttpExtension().GetVerb().String() + " " + req.Message().GetMethod(),
				ids:  req.Metadata()[transport.ActivationIDHeader].GetValues(),
			})
			calls.lock.Unlock()
			return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
		}),
	}
	for _, opt := range opts {
		opt(&o)
	}

	return New(o), calls
}

func invokeFoo(t *testing.T, target targets.Interface) {
	t.Helper()
	_, err := target.InvokeMethod(t.Context(), internalv1pb.NewInternalInvokeRequest("foo").WithActor("abc", target.ID()))
	require.NoError(t, err)
}

func activationID(t *testing.T, calls *callLog) string {
	t.Helper()
	got := calls.take()
	require.Len(t, got, 1)
	require.Len(t, got[0].ids, 1)
	require.NotEmpty(t, got[0].ids[0])
	return got[0].ids[0]
}

func Test_ActivationIDOnEveryCallback(t *testing.T) {
	t.Parallel()

	fact, calls := newActivationFactory(t)
	target := fact.GetOrCreate("a")
	reminder := &api.Reminder{ActorType: "abc", ActorID: "a", Name: "r"}

	invokeFoo(t, target)
	require.NoError(t, target.InvokeReminder(t.Context(), reminder))
	require.NoError(t, target.InvokeTimer(t.Context(), reminder))
	invokeFoo(t, target)

	got := calls.take()
	require.Len(t, got, 4)
	assert.Equal(t, "PUT actors/abc/a/method/foo", got[0].call)
	assert.Equal(t, "PUT actors/abc/a/method/remind/r", got[1].call)
	assert.Equal(t, "PUT actors/abc/a/method/timer/r", got[2].call)
	assert.Equal(t, "PUT actors/abc/a/method/foo", got[3].call)
	require.Len(t, got[0].ids, 1)
	require.NotEmpty(t, got[0].ids[0])
	for _, c := range got[1:] {
		assert.Equal(t, got[0].ids, c.ids, c.call)
	}

	invokeFoo(t, fact.GetOrCreate("b"))
	assert.NotEqual(t, got[0].ids[0], activationID(t, calls))
}

func Test_ActivationIDChangesPerActivation(t *testing.T) {
	t.Parallel()

	tests := map[string]func(t *testing.T, fact targets.Factory){
		"deactivate": func(t *testing.T, fact targets.Factory) {
			require.NoError(t, fact.GetOrCreate("a").Deactivate(t.Context()))
		},
		"halt all": func(t *testing.T, fact targets.Factory) {
			require.NoError(t, fact.HaltAll(t.Context()))
		},
		"halt non hosted": func(t *testing.T, fact targets.Factory) {
			require.NoError(t, fact.HaltNonHosted(t.Context(), func(*api.LookupActorRequest) bool { return false }))
		},
	}

	for name, deactivate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fact, calls := newActivationFactory(t)
			invokeFoo(t, fact.GetOrCreate("a"))
			first := activationID(t, calls)

			deactivate(t, fact)
			got := calls.take()
			require.Len(t, got, 1)
			assert.Equal(t, "DELETE actors/abc/a", got[0].call)

			invokeFoo(t, fact.GetOrCreate("a"))
			assert.NotEqual(t, first, activationID(t, calls))
		})
	}

	t.Run("idle", func(t *testing.T) {
		t.Parallel()

		clock := clocktesting.NewFakeClock(time.Now())
		fact, calls := newActivationFactory(t, func(o *Options) {
			o.clock = clock
			o.IdleTimeout = 10 * time.Second
		})

		invokeFoo(t, fact.GetOrCreate("a"))
		first := activationID(t, calls)

		require.Eventually(t, func() bool {
			clock.Step(10 * time.Second)
			return slices.ContainsFunc(calls.peek(), func(c callback) bool {
				return c.call == "DELETE actors/abc/a"
			})
		}, 5*time.Second, 10*time.Millisecond)
		calls.take()

		invokeFoo(t, fact.GetOrCreate("a"))
		assert.NotEqual(t, first, activationID(t, calls))
	})

	t.Run("new factory", func(t *testing.T) {
		t.Parallel()

		fact, calls := newActivationFactory(t)
		invokeFoo(t, fact.GetOrCreate("a"))
		first := activationID(t, calls)

		next, nextCalls := newActivationFactory(t)
		invokeFoo(t, next.GetOrCreate("a"))
		assert.NotEqual(t, first, activationID(t, nextCalls))
	})
}

func Test_GetOrCreateRaceQueuesWinner(t *testing.T) {
	t.Parallel()

	clock := clocktesting.NewFakeClock(time.Now())
	fact, _ := newActivationFactory(t, func(o *Options) {
		o.clock = clock
		o.IdleTimeout = 10 * time.Second
	})

	winners := make([]*app, 50)
	for i := range winners {
		id := strconv.Itoa(i)
		start := make(chan struct{})
		got := make([]targets.Interface, 8)
		var wg sync.WaitGroup
		for j := range got {
			wg.Go(func() {
				<-start
				got[j] = fact.GetOrCreate(id)
			})
		}
		close(start)
		wg.Wait()
		for _, g := range got {
			require.Same(t, got[0], g)
		}
		winners[i] = got[0].(*app)
	}

	require.Eventually(t, func() bool {
		clock.Step(10 * time.Second)
		return fact.Len() == 0 && !slices.ContainsFunc(winners, func(a *app) bool {
			return !a.closed.Load()
		})
	}, 5*time.Second, 10*time.Millisecond)
}

func Test_ActivationIDOverridesCaller(t *testing.T) {
	t.Parallel()

	var other atomic.Pointer[[]string]
	fact, calls := newActivationFactory(t, func(o *Options) {
		inner := o.AppChannel
		o.AppChannel = fake.New().WithInvokeMethod(func(ctx context.Context, req *invokev1.InvokeMethodRequest, appID string) (*invokev1.InvokeMethodResponse, error) {
			other.Store(new(req.Metadata()["other"].GetValues()))
			return inner.InvokeMethod(ctx, req, appID)
		})
	})
	target := fact.GetOrCreate("a")

	invokeFoo(t, target)
	want := activationID(t, calls)

	req := internalv1pb.NewInternalInvokeRequest("foo").WithActor("abc", "a").WithMetadata(map[string][]string{
		"dapr-actor-activation-id": {"spoofed"},
		"DAPR-ACTOR-ACTIVATION-ID": {"spoofed"},
		"other":                    {"kept"},
	})
	_, err := target.InvokeMethod(t.Context(), req)
	require.NoError(t, err)
	assert.Equal(t, want, activationID(t, calls))
	assert.Equal(t, []string{"kept"}, *other.Load())
	assert.Equal(t, []string{"DAPR-ACTOR-ACTIVATION-ID", "dapr-actor-activation-id", "other"}, slices.Sorted(maps.Keys(req.GetMetadata())))
	assert.Equal(t, []string{"spoofed"}, req.GetMetadata()["dapr-actor-activation-id"].GetValues())
}

func Test_InvokeMethodCopiesMessage(t *testing.T) {
	t.Parallel()

	data := &anypb.Any{Value: []byte("data")}
	tests := map[string]struct {
		ext  *commonv1pb.HTTPExtension
		want *commonv1pb.HTTPExtension
	}{
		"with http extension": {
			ext:  &commonv1pb.HTTPExtension{Verb: commonv1pb.HTTPExtension_POST, Querystring: "a=b"},
			want: &commonv1pb.HTTPExtension{Verb: commonv1pb.HTTPExtension_PUT, Querystring: "a=b"},
		},
		"without http extension": {
			want: &commonv1pb.HTTPExtension{Verb: commonv1pb.HTTPExtension_PUT},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			msg := &commonv1pb.InvokeRequest{
				Method:        "foo",
				Data:          data,
				ContentType:   "text/plain",
				HttpExtension: test.ext,
			}
			want := proto.Clone(msg)

			var got *commonv1pb.InvokeRequest
			fact, _ := newActivationFactory(t, func(o *Options) {
				o.AppChannel = fake.New().WithInvokeMethod(func(_ context.Context, req *invokev1.InvokeMethodRequest, _ string) (*invokev1.InvokeMethodResponse, error) {
					got = proto.Clone(req.Message()).(*commonv1pb.InvokeRequest)
					assert.NotSame(t, msg, req.Message())
					assert.Equal(t, "foo", msg.GetMethod())
					assert.Equal(t, test.ext.GetVerb(), msg.GetHttpExtension().GetVerb())
					assert.Same(t, data, req.Message().GetData())
					return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
				})
			})

			var changed atomic.Bool
			done := make(chan struct{})
			read := make(chan struct{})
			go func() {
				defer close(read)
				for {
					select {
					case <-done:
						return
					default:
						if msg.GetMethod() != "foo" || msg.GetHttpExtension().GetVerb() != test.ext.GetVerb() {
							changed.Store(true)
						}
					}
				}
			}()

			_, err := fact.GetOrCreate("a").InvokeMethod(t.Context(), &internalv1pb.InternalInvokeRequest{
				Ver:     internalv1pb.APIVersion_V1,
				Actor:   &internalv1pb.Actor{ActorType: "abc", ActorId: "a"},
				Message: msg,
			})
			close(done)
			<-read
			require.NoError(t, err)
			assert.False(t, changed.Load())

			assert.Equal(t, "actors/abc/a/method/foo", got.GetMethod())
			assert.Equal(t, "text/plain", got.GetContentType())
			assert.True(t, proto.Equal(test.want, got.GetHttpExtension()), "%v", got.GetHttpExtension())
			assert.True(t, proto.Equal(want, msg), "%v", msg)
		})
	}
}

func Test_ActivationIDReentrant(t *testing.T) {
	t.Parallel()

	store := reentrancystore.New()
	store.Store("abc", config.ReentrancyConfig{Enabled: true})
	fact, calls := newActivationFactory(t, func(o *Options) { o.Reentrancy = store })
	target := fact.GetOrCreate("a")

	invokeFoo(t, target)
	want := activationID(t, calls)

	req := internalv1pb.NewInternalInvokeRequest("foo").WithActor("abc", "a").WithMetadata(map[string][]string{
		"Dapr-Reentrancy-Id": {"r1"},
	})
	_, err := target.InvokeMethod(t.Context(), req)
	require.NoError(t, err)
	assert.Equal(t, want, activationID(t, calls))
	assert.Equal(t, []string{"r1"}, req.GetMetadata()["Dapr-Reentrancy-Id"].GetValues())
	assert.NotContains(t, req.GetMetadata(), transport.ActivationIDHeader)
}

func newTimerTestActor(t *testing.T, fn func(ctx context.Context, method string)) *app {
	t.Helper()
	fact := New(Options{
		ActorType:   "abc",
		Reentrancy:  reentrancystore.New(),
		IdleTimeout: time.Hour,
		Resiliency:  resiliency.New(logger.NewLogger("test")),
		AppChannel: fake.New().WithInvokeMethod(func(ctx context.Context, req *invokev1.InvokeMethodRequest, _ string) (*invokev1.InvokeMethodResponse, error) {
			fn(ctx, req.Message().GetMethod())
			return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
		}),
	})
	return fact.GetOrCreate("foo").(*app)
}

func newRemovableTimer() (*api.Reminder, context.CancelFunc) {
	removed, remove := context.WithCancel(context.Background())
	return &api.Reminder{ActorType: "abc", ActorID: "foo", Name: "tick", IsTimer: true, Removed: removed}, remove
}

func TestInvokeTimerAlreadyRemovedDropped(t *testing.T) {
	t.Parallel()

	var timerCalls atomic.Int64
	a := newTimerTestActor(t, func(context.Context, string) { timerCalls.Add(1) })

	timer, remove := newRemovableTimer()
	remove()
	require.ErrorIs(t, a.InvokeTimer(t.Context(), timer), actorerrors.ErrTimerFireStale)
	assert.Zero(t, timerCalls.Load())
}

func TestInvokeTimerRemovedDuringCallbackCompletes(t *testing.T) {
	t.Parallel()

	timer, remove := newRemovableTimer()
	var called, alive atomic.Bool
	a := newTimerTestActor(t, func(ctx context.Context, _ string) {
		called.Store(true)
		remove()
		alive.Store(ctx.Err() == nil)
	})

	require.NoError(t, a.InvokeTimer(t.Context(), timer))
	assert.True(t, called.Load())
	assert.True(t, alive.Load(), "removing the timer cancelled its running callback")
}

func Test_ActivationWaitsForPreviousDelete(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		idle    bool
		trigger func(targets.Factory) error
	}{
		"halt all": {trigger: func(fact targets.Factory) error {
			return fact.HaltAll(context.Background())
		}},
		"halt non hosted": {trigger: func(fact targets.Factory) error {
			return fact.HaltNonHosted(context.Background(), func(*api.LookupActorRequest) bool { return false })
		}},
		"idle": {idle: true},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var lock sync.Mutex
			var events []string
			record := func(e string) {
				lock.Lock()
				defer lock.Unlock()
				events = append(events, e)
			}
			snapshot := func() []string {
				lock.Lock()
				defer lock.Unlock()
				return slices.Clone(events)
			}

			deleting := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			clock := clocktesting.NewFakeClock(time.Now())
			fact := New(Options{
				ActorType:   "abc",
				Reentrancy:  reentrancystore.New(),
				Resiliency:  resiliency.New(logger.NewLogger("test")),
				Placement:   placementfake.New(),
				IdleTimeout: 10 * time.Second,
				clock:       clock,
				AppChannel: fake.New().WithInvokeMethod(func(_ context.Context, req *invokev1.InvokeMethodRequest, _ string) (*invokev1.InvokeMethodResponse, error) {
					if req.Message().GetHttpExtension().GetVerb().String() == http.MethodDelete {
						record("DELETE start")
						once.Do(func() { close(deleting) })
						<-release
						record("DELETE done")
					} else {
						record("PUT " + req.Metadata()[transport.ActivationIDHeader].GetValues()[0])
					}
					return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
				}),
			})

			old := fact.GetOrCreate("a")
			invokeFoo(t, old)

			halted := make(chan error, 1)
			if test.idle {
				require.Eventually(t, func() bool {
					clock.Step(10 * time.Second)
					select {
					case <-deleting:
						return true
					default:
						return false
					}
				}, 5*time.Second, 10*time.Millisecond)
				close(halted)
			} else {
				go func() { halted <- test.trigger(fact) }()
				select {
				case <-deleting:
				case <-time.After(5 * time.Second):
					require.Fail(t, "DELETE never started")
				}
			}

			_, err := old.InvokeMethod(t.Context(), internalv1pb.NewInternalInvokeRequest("foo").WithActor("abc", "a"))
			require.Error(t, err)
			assert.True(t, targetserrors.IsClosed(err), err)

			invoked := make(chan error, 1)
			go func() {
				_, err := fact.GetOrCreate("a").InvokeMethod(t.Context(), internalv1pb.NewInternalInvokeRequest("foo").WithActor("abc", "a"))
				invoked <- err
			}()

			select {
			case err := <-invoked:
				require.Failf(t, "new activation ran before the old DELETE returned", "err=%v events=%v", err, snapshot())
			case <-time.After(200 * time.Millisecond):
			}

			close(release)
			select {
			case err := <-invoked:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				require.Fail(t, "new activation never ran")
			}
			if err, ok := <-halted; ok {
				require.NoError(t, err)
			}

			got := snapshot()
			require.Len(t, got, 4, got)
			assert.Equal(t, []string{"DELETE start", "DELETE done"}, got[1:3])
			assert.True(t, strings.HasPrefix(got[0], "PUT "), got)
			assert.True(t, strings.HasPrefix(got[3], "PUT "), got)
			assert.NotEqual(t, got[0], got[3])
		})
	}
}
