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

package callbackstream

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/config"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// runManager spins up a Manager with its event loop attached to a per-test
// context, returning the manager. The loop goroutine is reaped via
// t.Cleanup so it doesn't leak across tests sharing the package-level
// loopFactory.
func runManager(t *testing.T) *Manager {
	t.Helper()
	mgr := NewManager()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = mgr.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return mgr
}

// echoConn launches a goroutine that drains conn.Outbox and immediately
// echoes back through Deliver with the supplied reply builder. It returns
// when conn is closed. Used as a minimal stand-in for the gRPC handler's
// send/recv loops. Keeping the goroutine free of t.Fatal is what
// lets the loopcheck linter keep quiet.
func echoConn(t *testing.T, conn *Connection, reply func(id string) *runtimev1pb.SubscribeActorEventsRequestAlpha1) {
	t.Helper()
	go func() {
		for {
			select {
			case <-conn.Done():
				return
			case out := <-conn.Outbox:
				id := out.GetInvokeRequest().GetId()
				if id == "" {
					t.Errorf("outbound message had no correlation id: %T", out.GetResponseType())
					return
				}
				conn.Deliver(reply(id))
			}
		}
	}()
}

// invokeBuild builds a minimal invoke-request response message. The
// callbackstream package only cares about the correlation id, not the
// payload — these helpers keep the test code uncluttered.
func invokeBuild(actorType string) func(string) *runtimev1pb.SubscribeActorEventsResponseAlpha1 {
	return func(id string) *runtimev1pb.SubscribeActorEventsResponseAlpha1 {
		return &runtimev1pb.SubscribeActorEventsResponseAlpha1{
			ResponseType: &runtimev1pb.SubscribeActorEventsResponseAlpha1_InvokeRequest{
				InvokeRequest: &runtimev1pb.SubscribeActorEventsResponseInvokeRequestAlpha1{
					Id:        id,
					ActorType: actorType,
				},
			},
		}
	}
}

// invokeReply mirrors the app's response side: an InvokeResponse whose id
// matches a previous request.
func invokeReply(id string) *runtimev1pb.SubscribeActorEventsRequestAlpha1 {
	return &runtimev1pb.SubscribeActorEventsRequestAlpha1{
		RequestType: &runtimev1pb.SubscribeActorEventsRequestAlpha1_InvokeResponse{
			InvokeResponse: &runtimev1pb.SubscribeActorEventsRequestInvokeResponseAlpha1{Id: id},
		},
	}
}

func TestSend_NoConnectionReturnsErr(t *testing.T) {
	mgr := runManager(t)

	_, err := mgr.Send(t.Context(), invokeBuild("typeA"))
	assert.ErrorIs(t, err, ErrNoConnection)
}

// TestRegister_MultipleConnsRouteToLatest verifies the LIFO routing: when
// several apps stream into daprd at once, the most recently registered
// connection serves new callbacks. This matters during overlapping
// reconnects (kubernetes rolling restarts, in-place upgrades).
func TestRegister_MultipleConnsRouteToLatest(t *testing.T) {
	mgr := runManager(t)

	c1 := mgr.Register(t.Context(), &config.ApplicationConfig{Entities: []string{"v1"}})
	c2 := mgr.Register(t.Context(), &config.ApplicationConfig{Entities: []string{"v2"}})
	t.Cleanup(func() {
		mgr.Close(c1, nil)
		mgr.Close(c2, nil)
	})

	// CurrentConfig must reflect the most recent registrant.
	assert.Equal(t, []string{"v2"}, mgr.CurrentConfig().Entities)
	assert.True(t, mgr.HasConnection())

	// Send goes to c2's outbox, not c1's.
	echoConn(t, c2, invokeReply)
	resp, err := mgr.Send(t.Context(), invokeBuild("typeA"))
	require.NoError(t, err)
	require.NotNil(t, resp.GetInvokeResponse())

	// c1 must have received nothing.
	select {
	case msg := <-c1.Outbox:
		t.Fatalf("c1 should not have received any message, got %T", msg.GetResponseType())
	case <-time.After(50 * time.Millisecond):
	}
}

// TestClose_LatestFallsBackToPrevious verifies that closing the active
// connection while an earlier one is still live routes traffic to the
// previous connection rather than failing with ErrNoConnection.
func TestClose_LatestFallsBackToPrevious(t *testing.T) {
	mgr := runManager(t)

	c1 := mgr.Register(t.Context(), &config.ApplicationConfig{Entities: []string{"v1"}})
	c2 := mgr.Register(t.Context(), &config.ApplicationConfig{Entities: []string{"v2"}})
	t.Cleanup(func() { mgr.Close(c1, nil) })

	mgr.Close(c2, nil)

	// Snapshot must now reflect c1.
	assert.Eventually(t, func() bool {
		cfg := mgr.CurrentConfig()
		return cfg != nil && len(cfg.Entities) == 1 && cfg.Entities[0] == "v1"
	}, time.Second, 10*time.Millisecond, "snapshot did not fall back to previous connection")

	echoConn(t, c1, invokeReply)
	resp, err := mgr.Send(t.Context(), invokeBuild("typeA"))
	require.NoError(t, err)
	require.NotNil(t, resp.GetInvokeResponse())
}

// TestClose_NonLatestDoesNotPerturb verifies that closing an older
// connection while a newer one is active doesn't change routing. The
// snapshot must still point at the latest connection.
func TestClose_NonLatestDoesNotPerturb(t *testing.T) {
	mgr := runManager(t)

	c1 := mgr.Register(t.Context(), &config.ApplicationConfig{Entities: []string{"v1"}})
	c2 := mgr.Register(t.Context(), &config.ApplicationConfig{Entities: []string{"v2"}})
	t.Cleanup(func() { mgr.Close(c2, nil) })

	mgr.Close(c1, nil)

	assert.Eventually(t, func() bool {
		cfg := mgr.CurrentConfig()
		return cfg != nil && len(cfg.Entities) == 1 && cfg.Entities[0] == "v2"
	}, time.Second, 10*time.Millisecond, "snapshot regressed after closing non-latest connection")

	echoConn(t, c2, invokeReply)
	resp, err := mgr.Send(t.Context(), invokeBuild("typeA"))
	require.NoError(t, err)
	require.NotNil(t, resp.GetInvokeResponse())
}

// TestClose_AllDisconnectsResetsState verifies that once every connection
// is gone, the manager looks brand-new: HasConnection is false,
// CurrentConfig is nil, and Send fails with ErrNoConnection.
func TestClose_AllDisconnectsResetsState(t *testing.T) {
	mgr := runManager(t)

	c1 := mgr.Register(t.Context(), &config.ApplicationConfig{Entities: []string{"v1"}})
	c2 := mgr.Register(t.Context(), &config.ApplicationConfig{Entities: []string{"v2"}})

	mgr.Close(c1, nil)
	mgr.Close(c2, nil)

	assert.Eventually(t, func() bool {
		return !mgr.HasConnection() && mgr.CurrentConfig() == nil
	}, time.Second, 10*time.Millisecond)

	_, err := mgr.Send(t.Context(), invokeBuild("typeA"))
	assert.ErrorIs(t, err, ErrNoConnection)
}

// TestSend_InflightFailsOnConnClose verifies that an in-flight Send
// observing a Close gets ErrDisconnected (the wrapped cause), not a
// hung-forever channel. This is the disconnect-during-call path that
// matters when the app crashes mid-invocation.
func TestSend_InflightFailsOnConnClose(t *testing.T) {
	mgr := runManager(t)

	conn := mgr.Register(t.Context(), &config.ApplicationConfig{Entities: []string{"v1"}})

	// Start a Send and let it land in the outbox without responding.
	type result struct {
		msg *runtimev1pb.SubscribeActorEventsRequestAlpha1
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		msg, err := mgr.Send(t.Context(), invokeBuild("typeA"))
		resCh <- result{msg, err}
	}()

	// Drain (but don't reply) to release the outbox-send branch — Send is
	// now waiting on the response channel.
	select {
	case <-conn.Outbox:
	case <-time.After(time.Second):
		t.Fatal("Send did not push to outbox in time")
	}

	mgr.Close(conn, nil)

	select {
	case r := <-resCh:
		assert.Nil(t, r.msg)
		require.ErrorIs(t, r.err, ErrDisconnected)
	case <-time.After(2 * time.Second):
		t.Fatal("Send did not return after Close")
	}
}

// TestMultipleConns_OlderConnDrainsInFlight is the regression guard for
// the documented rolling-restart semantic: once a newer connection
// registers, every new Send goes to it, but Sends that the older
// connection already accepted (the request message has landed in its
// Outbox) still complete on the older connection. Pod A finishes its
// in-flight work while every new callback goes to pod B.
func TestMultipleConns_OlderConnDrainsInFlight(t *testing.T) {
	mgr := runManager(t)

	c1 := mgr.Register(t.Context(), &config.ApplicationConfig{Entities: []string{"v1"}})
	t.Cleanup(func() { mgr.Close(c1, nil) })

	// Start an in-flight Send on c1 but don't drain its Outbox yet — the
	// request sits buffered on c1, awaiting a reply.
	type result struct {
		msg *runtimev1pb.SubscribeActorEventsRequestAlpha1
		err error
	}
	c1ResCh := make(chan result, 1)
	go func() {
		msg, err := mgr.Send(t.Context(), invokeBuild("typeA-on-c1"))
		c1ResCh <- result{msg, err}
	}()

	// Wait for the request to actually land in c1's Outbox before the
	// second connection arrives, so we know c1 owns this in-flight Send.
	var inflightOnC1 *runtimev1pb.SubscribeActorEventsResponseAlpha1
	select {
	case inflightOnC1 = <-c1.Outbox:
	case <-time.After(time.Second):
		t.Fatal("inflight Send did not reach c1 outbox")
	}

	// New connection takes over — the snapshot must flip to c2 so further
	// Sends route there, not back to c1.
	c2 := mgr.Register(t.Context(), &config.ApplicationConfig{Entities: []string{"v2"}})
	t.Cleanup(func() { mgr.Close(c2, nil) })

	// New Send must land on c2, not c1.
	c2ResCh := make(chan result, 1)
	go func() {
		msg, err := mgr.Send(t.Context(), invokeBuild("typeA-on-c2"))
		c2ResCh <- result{msg, err}
	}()
	var newOnC2 *runtimev1pb.SubscribeActorEventsResponseAlpha1
	select {
	case newOnC2 = <-c2.Outbox:
	case <-time.After(time.Second):
		t.Fatal("new Send did not reach c2 outbox after c2 registered")
	}
	// Sanity: c1 must not have received the new request.
	select {
	case spurious := <-c1.Outbox:
		t.Fatalf("c1 should not see new traffic after c2 registered, got %T", spurious.GetResponseType())
	case <-time.After(50 * time.Millisecond):
	}

	// Now complete both: c1 first to prove its in-flight wasn't lost, then
	// c2 to prove the new request was routed there. Order matters as the
	// test of the semantic — pod A drains its tail.
	c1.Deliver(invokeReply(inflightOnC1.GetInvokeRequest().GetId()))
	select {
	case r := <-c1ResCh:
		require.NoError(t, r.err)
		require.NotNil(t, r.msg.GetInvokeResponse())
	case <-time.After(time.Second):
		t.Fatal("c1 in-flight Send never returned after Deliver")
	}

	c2.Deliver(invokeReply(newOnC2.GetInvokeRequest().GetId()))
	select {
	case r := <-c2ResCh:
		require.NoError(t, r.err)
		require.NotNil(t, r.msg.GetInvokeResponse())
	case <-time.After(time.Second):
		t.Fatal("c2 Send never returned after Deliver")
	}
}

// TestRegister_AfterFullDisconnectRestoresRouting verifies that the
// manager recovers when an app drops all streams and then reconnects —
// the Kubernetes pod-restart scenario. Without this, a daprd that lost
// its app would never serve callbacks again until restart.
func TestRegister_AfterFullDisconnectRestoresRouting(t *testing.T) {
	mgr := runManager(t)

	c1 := mgr.Register(t.Context(), &config.ApplicationConfig{Entities: []string{"v1"}})
	mgr.Close(c1, nil)

	assert.Eventually(t, func() bool {
		return !mgr.HasConnection()
	}, time.Second, 10*time.Millisecond)

	c2 := mgr.Register(t.Context(), &config.ApplicationConfig{Entities: []string{"v1-reconnected"}})
	t.Cleanup(func() { mgr.Close(c2, nil) })

	assert.True(t, mgr.HasConnection())
	assert.Equal(t, []string{"v1-reconnected"}, mgr.CurrentConfig().Entities)

	echoConn(t, c2, invokeReply)
	resp, err := mgr.Send(t.Context(), invokeBuild("typeA"))
	require.NoError(t, err)
	require.NotNil(t, resp.GetInvokeResponse())
}

// TestRegisterClose_Concurrent exercises the loop's serialization under
// concurrent register/close churn. With locks removed in favour of the
// kit/events/loop pattern, this is the regression guard against races.
// Run with -race.
func TestRegisterClose_Concurrent(t *testing.T) {
	mgr := runManager(t)

	const goroutines = 16
	const iterations = 25

	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Go(func() {
			cfg := &config.ApplicationConfig{Entities: []string{"v" + strconv.Itoa(i)}}
			for range iterations {
				conn := mgr.Register(t.Context(), cfg)
				// Touch the snapshot so the read path is also exercised.
				_ = mgr.HasConnection()
				_ = mgr.CurrentConfig()
				mgr.Close(conn, nil)
			}
		})
	}
	wg.Wait()

	// After everything closes, no connection should remain.
	assert.Eventually(t, func() bool {
		return !mgr.HasConnection() && mgr.CurrentConfig() == nil
	}, 2*time.Second, 10*time.Millisecond)
}

// TestSend_ConcurrentCallersFanOutToSingleConn verifies that several
// concurrent Sends through a single connection each get their own
// correlated reply — the per-connection sync.Map indexed by request id
// is what makes this safe without serializing the hot path.
func TestSend_ConcurrentCallersFanOutToSingleConn(t *testing.T) {
	mgr := runManager(t)

	conn := mgr.Register(t.Context(), &config.ApplicationConfig{Entities: []string{"v1"}})
	t.Cleanup(func() { mgr.Close(conn, nil) })

	const callers = 32
	var delivered atomic.Int32

	// Single drainer goroutine acts as the app: read each request, reply
	// with the same id. Counts replies via delivered. Exits when conn is
	// closed by the cleanup hook.
	go func() {
		for {
			select {
			case <-conn.Done():
				return
			case out := <-conn.Outbox:
				id := out.GetInvokeRequest().GetId()
				conn.Deliver(invokeReply(id))
				delivered.Add(1)
			}
		}
	}()

	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Go(func() {
			_, err := mgr.Send(t.Context(), invokeBuild("typeA"))
			errs <- err
		})
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	assert.EqualValues(t, callers, delivered.Load())
}
