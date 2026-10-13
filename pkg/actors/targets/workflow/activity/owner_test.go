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

package activity

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/kit/crypto/spiffe/signer"

	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity/inflight"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common/detached"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/signing"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// Test_runOwned_handoffPublishIsOwned pins the ownership of the publish
// handed off when the owner's ctx cancels mid-execution. It used to run in a
// bare goroutine belonging to neither drain scope, so it could publish into a
// parent workflow after the factory believed it had drained, and a callback
// that never arrived parked it forever. It now belongs to the factory's
// runtime-lifetime runner, which both waits for it and reclaims it.
func Test_runOwned_handoffPublishIsOwned(t *testing.T) {
	t.Parallel()

	inflightKey := func() string {
		return inflight.Key("wf::3::0", testInvocation().GetHistoryEvent())
	}

	// handOff runs one execution up to the WorkItem dispatch, then cancels
	// the owner so runOwned hands the still-queued WorkItem to the watcher.
	handOff := func(t *testing.T, f *factory, scheduled chan *backend.ActivityWorkItem) *activityHandoff {
		t.Helper()
		a := f.GetOrCreate("wf::3::0").(*activity)
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		ownerErr := make(chan error, 1)
		go func() {
			ownerErr <- a.executeActivity(ctx, testReminder(), testInvocation())
		}()

		wi := recvWorkItem(t, scheduled)
		call, ok := f.inflight.Peek(inflightKey())
		require.True(t, ok, "the dispatched execution must hold its inflight entry")

		cancel()
		require.ErrorIs(t, <-ownerErr, context.Canceled, "the owner surfaces the cancellation to its caller")
		return &activityHandoff{wi: wi, call: call}
	}

	t.Run("the runner drains a handed-off publish", func(t *testing.T) {
		t.Parallel()
		f, scheduled := newExecHarness(t)
		published := make(chan *internalsv1pb.InternalInvokeRequest, 2)
		f.router = routerfake.New().WithCallFn(func(_ context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			published <- req
			return &internalsv1pb.InternalInvokeResponse{}, nil
		})

		h := handOff(t, f, scheduled)

		// The owner is gone but the WorkItem is still in the engine queue.
		// Its completion must be published by a goroutine the factory
		// accounts for, so waiting on the runner is enough to see it land: a
		// bare goroutine would leave the runner reporting drained with
		// nothing published.
		completeWorkItem(t, h.wi)
		f.detached.Wait()

		require.Len(t, published, 1, "the runner must not report drained before the handed-off publish has landed")
		require.True(t, h.call.Settled(), "the watcher finalises the inflight entry")
		require.NoError(t, h.call.Err())
	})

	t.Run("shutdown reclaims a watcher whose callback never arrives", func(t *testing.T) {
		t.Parallel()
		f, scheduled := newExecHarness(t)
		rootCtx, rootCancel := context.WithCancel(t.Context())
		f.detached = detached.New(rootCtx)

		h := handOff(t, f, scheduled)

		// The app never reports. Shutdown must reclaim the watcher instead of
		// leaking it, and settle the entry so parked followers retry rather
		// than waiting on a call nothing will ever report.
		rootCancel()
		f.detached.Wait()

		require.True(t, h.call.Settled())
		require.ErrorIs(t, h.call.Err(), errPublishAbandoned)
		_, ok := f.inflight.Peek(inflightKey())
		assert.False(t, ok, "an abandoned publish releases the entry so a retry becomes a fresh owner")
	})

	t.Run("a hand-off after shutdown is abandoned, not dropped", func(t *testing.T) {
		t.Parallel()
		f, scheduled := newExecHarness(t)
		rootCtx, rootCancel := context.WithCancel(t.Context())
		rootCancel()
		f.detached = detached.New(rootCtx)

		h := handOff(t, f, scheduled)
		f.detached.Wait()

		require.True(t, h.call.Settled(), "the runner refuses the work; the entry must still be settled")
		require.ErrorIs(t, h.call.Err(), errPublishAbandoned)
	})
}

type activityHandoff struct {
	wi   *backend.ActivityWorkItem
	call *inflight.Call
}

// Test_runOwned_inHandResultOutlivesCaller pins that a result the owner holds
// is published even when the ctx of the call waiting on it is already cut
// (client disconnect, invocation timeout, placement drain). It used to be
// published under that ctx, so the publish failed, the entry was released
// with the error and the retry re-ran a body whose result was in hand.
func Test_runOwned_inHandResultOutlivesCaller(t *testing.T) {
	t.Parallel()

	key := inflight.Key("wf::3::0", testInvocation().GetHistoryEvent())

	t.Run("a cancelled caller still gets its result published", func(t *testing.T) {
		t.Parallel()
		f, scheduled := newExecHarness(t)
		published := make(chan *internalsv1pb.InternalInvokeRequest, 2)
		f.router = routerfake.New().WithCallFn(func(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			published <- req
			return &internalsv1pb.InternalInvokeResponse{}, nil
		})

		a := f.GetOrCreate("wf::3::0").(*activity)
		ctx, cancel := context.WithCancel(t.Context())
		ownerErr := make(chan error, 1)
		go func() {
			ownerErr <- a.executeActivity(ctx, testReminder(), testInvocation())
		}()
		wi := recvWorkItem(t, scheduled)
		call, ok := f.inflight.Peek(key)
		require.True(t, ok)

		// Cut the caller and hand the result over in the same instant: the
		// owner may observe either first, and the publish must land in both
		// orders.
		cancel()
		completeWorkItem(t, wi)

		err := <-ownerErr
		if err != nil {
			require.ErrorIs(t, err, context.Canceled)
		}
		f.detached.Wait()

		require.Len(t, published, 1, "the in-hand result must be published exactly once")
		require.True(t, call.Settled())
		require.NoError(t, call.Err(), "the entry caches the success so a retry acks instead of re-executing")
	})

	t.Run("a publish that fails releases the entry for recovery", func(t *testing.T) {
		t.Parallel()
		f, scheduled := newExecHarness(t)
		f.router = routerfake.New().WithCallFn(func(context.Context, *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			return nil, errors.New("parent unreachable")
		})

		a := f.GetOrCreate("wf::3::0").(*activity)
		ownerErr := make(chan error, 1)
		go func() {
			ownerErr <- a.executeActivity(t.Context(), testReminder(), testInvocation())
		}()
		wi := recvWorkItem(t, scheduled)
		call, ok := f.inflight.Peek(key)
		require.True(t, ok)

		completeWorkItem(t, wi)
		require.ErrorContains(t, <-ownerErr, "parent unreachable")
		f.detached.Wait()

		require.True(t, call.Settled())
		require.ErrorContains(t, call.Err(), "parent unreachable")
		_, ok = f.inflight.Peek(key)
		assert.False(t, ok, "a failed publish releases the entry so the retry re-executes as a fresh owner")
	})

	t.Run("a hand-in after shutdown is abandoned, not dropped", func(t *testing.T) {
		t.Parallel()
		f, scheduled := newExecHarness(t)
		rootCtx, rootCancel := context.WithCancel(t.Context())
		rootCancel()
		f.detached = detached.New(rootCtx)

		a := f.GetOrCreate("wf::3::0").(*activity)
		ownerErr := make(chan error, 1)
		go func() {
			ownerErr <- a.executeActivity(t.Context(), testReminder(), testInvocation())
		}()
		wi := recvWorkItem(t, scheduled)
		call, ok := f.inflight.Peek(key)
		require.True(t, ok)

		completeWorkItem(t, wi)
		require.ErrorIs(t, <-ownerErr, errPublishAbandoned)
		require.True(t, call.Settled())
		require.ErrorIs(t, call.Err(), errPublishAbandoned)
	})
}

// Test_runOwned_busyParentIsNotAFailedPublish pins that a result publish has
// no deadline of its own. The parent's AddWorkflowEvent waits for the
// parent's turn lock, which every completion of a fan-out queues on. A
// deadline on that wait settled the execution as failed, the run-activity
// reminder ran the body again, and each re-run queued on the same lock.
func Test_runOwned_busyParentIsNotAFailedPublish(t *testing.T) {
	t.Parallel()

	key := inflight.Key("wf::3::0", testInvocation().GetHistoryEvent())

	// busyParent takes the result only once release is closed, as a parent
	// whose turn lock is held. Each call reports on entered whether its
	// context carries a deadline.
	type busyParent struct {
		entered chan bool
		release chan struct{}
		calls   atomic.Int64
	}
	newBusyParent := func(f *factory) *busyParent {
		b := &busyParent{entered: make(chan bool, 4), release: make(chan struct{})}
		f.router = routerfake.New().WithCallFn(func(ctx context.Context, _ *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			b.calls.Add(1)
			_, hasDeadline := ctx.Deadline()
			b.entered <- hasDeadline
			select {
			case <-b.release:
				return &internalsv1pb.InternalInvokeResponse{}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
		return b
	}
	recvEntered := func(t *testing.T, b *busyParent) bool {
		t.Helper()
		select {
		case hasDeadline := <-b.entered:
			return hasDeadline
		case <-time.After(time.Second * 5):
			t.Fatal("the result was never published")
			return false
		}
	}

	t.Run("the owner's publish waits for a busy parent", func(t *testing.T) {
		t.Parallel()
		f, scheduled := newExecHarness(t)
		b := newBusyParent(f)

		a := f.GetOrCreate("wf::3::0").(*activity)
		ownerErr := make(chan error, 1)
		go func() {
			ownerErr <- a.executeActivity(t.Context(), testReminder(), testInvocation())
		}()
		wi := recvWorkItem(t, scheduled)
		call, ok := f.inflight.Peek(key)
		require.True(t, ok)

		completeWorkItem(t, wi)
		assert.False(t, recvEntered(t, b), "a deadline on the publish turns a busy parent into a failed execution")
		assert.False(t, call.Settled(), "the execution stays in flight while the parent is busy")

		close(b.release)
		require.NoError(t, <-ownerErr, "the owner reports the delivery once the parent takes it")
		f.detached.Wait()

		assert.Equal(t, int64(1), b.calls.Load(), "one delivery")
		assert.Empty(t, scheduled, "the body must not be dispatched again")
		require.True(t, call.Settled())
		require.NoError(t, call.Err(), "the entry caches the success so a retry acks instead of re-executing")
	})

	t.Run("a handed-off publish waits for a busy parent", func(t *testing.T) {
		t.Parallel()
		f, scheduled := newExecHarness(t)
		b := newBusyParent(f)

		// Cut the owner mid-execution so runOwned hands the queued WorkItem
		// to the watcher (watchAndPublish).
		a := f.GetOrCreate("wf::3::0").(*activity)
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)
		ownerErr := make(chan error, 1)
		go func() {
			ownerErr <- a.executeActivity(ctx, testReminder(), testInvocation())
		}()
		wi := recvWorkItem(t, scheduled)
		call, ok := f.inflight.Peek(key)
		require.True(t, ok)
		cancel()
		require.ErrorIs(t, <-ownerErr, context.Canceled)

		completeWorkItem(t, wi)
		assert.False(t, recvEntered(t, b), "a deadline on the publish turns a busy parent into a failed execution")

		close(b.release)
		f.detached.Wait()

		assert.Equal(t, int64(1), b.calls.Load(), "one delivery")
		assert.Empty(t, scheduled, "the body must not be dispatched again")
		require.True(t, call.Settled())
		require.NoError(t, call.Err())
	})

	t.Run("shutdown keeps the publish in flight for the grace", func(t *testing.T) {
		t.Parallel()
		f, scheduled := newExecHarness(t)
		rootCtx, rootCancel := context.WithCancel(t.Context())
		f.detached = detached.New(rootCtx)
		f.publishGrace = newPublishGrace(f.detached.Context(), time.Minute)
		b := newBusyParent(f)

		a := f.GetOrCreate("wf::3::0").(*activity)
		ownerErr := make(chan error, 1)
		go func() {
			ownerErr <- a.executeActivity(t.Context(), testReminder(), testInvocation())
		}()
		wi := recvWorkItem(t, scheduled)
		call, ok := f.inflight.Peek(key)
		require.True(t, ok)

		completeWorkItem(t, wi)
		recvEntered(t, b)
		// The runtime shuts down. The publish stays in flight, and the parent
		// then takes the result within the grace. The fake router ignores
		// shutdown, so this checks the grace timer only. In daprd the router
		// cancels the call at once (see publishAndSettle). The integration
		// tests busyparentshutdown and busyparentabandon cover that order.
		rootCancel()
		select {
		case err := <-ownerErr:
			t.Fatalf("shutdown ended the publish before the grace: %v", err)
		case <-time.After(time.Millisecond * 100):
		}
		close(b.release)
		require.NoError(t, <-ownerErr)
		f.detached.Wait()

		require.True(t, call.Settled())
		require.NoError(t, call.Err(), "a result the parent takes within the grace is delivered")
	})
}

// Test_runOwned_expiredSignatureSignedAgain pins what the sender does when the
// parent refuses a result because its signer certificate expired while the
// result waited for the parent. The publish has no deadline, so a busy parent
// can outlast the certificate. The sender signs the result again: with a newer
// certificate it delivers again, and with the same one it settles with a
// recoverable error, so the run-activity reminder retries later.
func Test_runOwned_expiredSignatureSignedAgain(t *testing.T) {
	t.Parallel()

	key := inflight.Key("wf::3::0", testInvocation().GetHistoryEvent())
	newSVID := func(t *testing.T) *x509svid.SVID {
		t.Helper()
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		template := &x509.Certificate{
			SerialNumber: big.NewInt(1),
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
			URIs:         []*url.URL{{Scheme: "spiffe", Host: "example.org", Path: "/ns/default/testapp"}},
		}
		der, err := x509.CreateCertificate(rand.Reader, template, template, pub, priv)
		require.NoError(t, err)
		certs, err := x509.ParseCertificates(der)
		require.NoError(t, err)
		id, err := x509svid.IDFromCert(certs[0])
		require.NoError(t, err)
		return &x509svid.SVID{ID: id, Certificates: certs, PrivateKey: priv}
	}
	// sent records the certificate chain that signed each delivery.
	signedWith := func(t *testing.T, req *internalsv1pb.InternalInvokeRequest) []byte {
		t.Helper()
		var e protos.HistoryEvent
		require.NoError(t, proto.Unmarshal(req.GetMessage().GetData().GetValue(), &e))
		return e.GetTaskCompleted().GetSignerCertificate()
	}
	refused := fmt.Errorf("rpc error: code = Unknown desc = activity completion for task 3: %w", signing.ErrSignerCertExpired)

	t.Run("a newer certificate is delivered again", func(t *testing.T) {
		t.Parallel()
		f, scheduled := newExecHarness(t)
		first, second := newSVID(t), newSVID(t)
		source := new(rotatingSVIDSource)
		source.svid.Store(first)
		f.signing = &signing.Signing{Signer: signer.New(source, nil), Namespace: "default"}

		var sent [][]byte
		f.router = routerfake.New().WithCallFn(func(_ context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			sent = append(sent, signedWith(t, req))
			if len(sent) == 1 {
				// The sidecar rotated its certificate while the result
				// waited for the parent.
				source.svid.Store(second)
				return nil, refused
			}
			return &internalsv1pb.InternalInvokeResponse{}, nil
		})

		a := f.GetOrCreate("wf::3::0").(*activity)
		ownerErr := make(chan error, 1)
		go func() {
			ownerErr <- a.executeActivity(t.Context(), testReminder(), testInvocation())
		}()
		wi := recvWorkItem(t, scheduled)
		call, ok := f.inflight.Peek(key)
		require.True(t, ok)
		completeWorkItem(t, wi)
		require.NoError(t, <-ownerErr)
		f.detached.Wait()

		require.Len(t, sent, 2, "the refused result is delivered again")
		assert.Equal(t, first.Certificates[0].Raw, sent[0])
		assert.Equal(t, second.Certificates[0].Raw, sent[1], "the result is signed again with the current certificate")
		assert.Empty(t, scheduled, "the body must not be dispatched again")
		require.True(t, call.Settled())
		require.NoError(t, call.Err())
	})

	t.Run("the same certificate is not delivered again", func(t *testing.T) {
		t.Parallel()
		f, scheduled := newExecHarness(t)
		source := new(rotatingSVIDSource)
		source.svid.Store(newSVID(t))
		f.signing = &signing.Signing{Signer: signer.New(source, nil), Namespace: "default"}

		var calls atomic.Int64
		f.router = routerfake.New().WithCallFn(func(context.Context, *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			calls.Add(1)
			return nil, refused
		})

		a := f.GetOrCreate("wf::3::0").(*activity)
		ownerErr := make(chan error, 1)
		go func() {
			ownerErr <- a.executeActivity(t.Context(), testReminder(), testInvocation())
		}()
		wi := recvWorkItem(t, scheduled)
		call, ok := f.inflight.Peek(key)
		require.True(t, ok)
		completeWorkItem(t, wi)
		err := <-ownerErr
		f.detached.Wait()

		require.Error(t, err)
		assert.True(t, signing.IsSignerCertExpired(err), "%v", err)
		assert.Equal(t, int64(1), calls.Load(), "signing again with the same certificate cannot succeed")
		require.True(t, call.Settled())
		require.Error(t, call.Err(), "the run-activity reminder retries the execution")
	})
}

type rotatingSVIDSource struct {
	svid atomic.Pointer[x509svid.SVID]
}

func (s *rotatingSVIDSource) GetX509SVID() (*x509svid.SVID, error) {
	return s.svid.Load(), nil
}

// Test_settle_finishesBeforeReleasing pins the settle ordering. Releasing the
// inflight entry before finishing its call opened a window in which a new
// arrival became owner of a fresh call while followers were still parked on
// the old one, and so dispatched a second work item for the same task. The
// probe blocks until the entry disappears, so it always samples the moment
// the release becomes visible: at that instant the call must already report.
func Test_settle_finishesBeforeReleasing(t *testing.T) {
	t.Parallel()
	f, _ := newExecHarness(t)

	for i := range 200 {
		key := "k" + strconv.Itoa(i)
		call, owner := f.inflight.Acquire(key)
		require.True(t, owner)

		var probes atomic.Int64
		settled := make(chan bool, 1)
		go func() {
			for {
				probes.Add(1)
				if _, present := f.inflight.Peek(key); !present {
					settled <- call.Settled()
					return
				}
			}
		}()
		// Only settle once the probe is demonstrably spinning, so it samples
		// the release/finish transition rather than the state after both.
		for probes.Load() < 100 {
			runtime.Gosched()
		}

		f.settle(key, call, errors.New("engine busy"))
		require.True(t, <-settled, "the inflight entry was released before its call was finished")
	}

	// The success path caches the outcome instead of releasing, so followers
	// arriving late still ack rather than dispatching a duplicate.
	call, owner := f.inflight.Acquire("cached")
	require.True(t, owner)
	f.settle("cached", call, nil)
	require.True(t, call.Settled())
	require.NoError(t, call.Err())
	cached, ok := f.inflight.Peek("cached")
	require.True(t, ok, "a successful outcome is cached for InflightCacheTTL")
	assert.Same(t, call, cached)
}
