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

package dissemination

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(coalesceaftercomplete))
}

// coalesceaftercomplete verifies the post-round coalesce window absorbs
// new connections that arrive AFTER the previous round has fully completed,
// not just ones queued while a round is in flight.
//
// This is the case the sibling `coalesce` test does NOT cover. In `coalesce`,
// stream B is connected mid-round so its ConnAdd is guaranteed to be in
// `waitingToDisseminate` at the moment the UNLOCK ack flips currentOperation
// back to REPORT. Here, B and C only connect after round 1 has been fully
// drained from the disseminator's event queue, exercising the path where the
// timer must be armed unconditionally on round completion - otherwise the
// first arriving stream takes the cold-start path and starts a fresh round
// alone, defeating coalescing.
type coalesceaftercomplete struct {
	place *placement.Placement
}

func (c *coalesceaftercomplete) Setup(t *testing.T) []framework.Option {
	c.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*10),
		placement.WithDisseminateCoalesceWindow(time.Millisecond*500),
	)

	return []framework.Option{
		framework.WithProcesses(c.place),
	}
}

func (c *coalesceaftercomplete) Run(t *testing.T, ctx context.Context) {
	c.place.WaitUntilRunning(t, ctx)

	assert.Eventually(t, func() bool {
		return c.place.IsLeader(t, ctx)
	}, time.Second*10, time.Millisecond*10)

	client := c.place.Client(t, ctx)

	// Stream A: drives round 1 to full completion before B or C exist.
	a, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, a.Send(&v1pb.Host{
		Name: "a", Port: 1001, Entities: []string{"actorA"}, Id: "a", Namespace: "default",
	}))

	// LOCK v1.
	r, err := a.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", r.GetOperation())
	require.NoError(t, a.Send(&v1pb.Host{Name: "a", Port: 1001, Entities: []string{"actorA"}, Id: "a", Namespace: "default"}))

	// UPDATE v1.
	r, err = a.Recv()
	require.NoError(t, err)
	require.Equal(t, "update", r.GetOperation())
	require.NoError(t, a.Send(&v1pb.Host{Name: "a", Port: 1001, Entities: []string{"actorA"}, Id: "a", Namespace: "default"}))

	// UNLOCK v1 — and ack it. After the server processes this ack, round 1
	// is fully complete with currentOperation back to REPORT and both
	// queues empty. The post-round coalesce timer must be armed here so
	// late-arriving streams within the window get coalesced.
	r, err = a.Recv()
	require.NoError(t, err)
	require.Equal(t, "unlock", r.GetOperation())
	v1 := r.GetVersion()
	require.NoError(t, a.Send(&v1pb.Host{Name: "a", Port: 1001, Entities: []string{"actorA"}, Id: "a", Namespace: "default"}))

	// Give the disseminator time to fully process the UNLOCK ack so that
	// when B connects, B's ConnAdd is ordered AFTER round-completion in
	// the event queue. Stay well inside the 500ms coalesce window so the
	// timer is still armed when B arrives.
	time.Sleep(time.Millisecond * 50)

	// Stream B: new actor type, connecting strictly after round 1 ended.
	// With the post-round timer armed, this should queue rather than
	// kicking off a fresh round on its own.
	b, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, b.Send(&v1pb.Host{
		Name: "b", Port: 1002, Entities: []string{"actorB"}, Id: "b", Namespace: "default",
	}))

	// Read from B in a goroutine. With the fix, B should NOT receive a
	// LOCK until either the timer expires or another arrival joins the
	// batch. Without the fix, B would receive LOCK almost immediately at
	// a version distinct from C's (each gets its own cold-start round).
	type recvResult struct {
		order *v1pb.PlacementOrder
		err   error
	}
	bRecv := make(chan recvResult, 1)
	go func() {
		r, rerr := b.Recv()
		bRecv <- recvResult{order: r, err: rerr}
	}()

	// 100ms is well inside the 500ms window and well above any plausible
	// disseminator processing latency. If LOCK arrives in this window the
	// timer was never armed and B took the cold-start path.
	select {
	case got := <-bRecv:
		switch {
		case got.err != nil:
			require.Failf(t, "stream B Recv failed before coalesce window expired",
				"got err=%v, expected post-round timer to defer the round", got.err)
		case got.order == nil:
			require.Fail(t, "stream B received nil order before coalesce window expired",
				"expected post-round timer to defer the round")
		default:
			require.Failf(t, "stream B received message before coalesce window expired",
				"got op=%s version=%d, expected post-round timer to defer the round",
				got.order.GetOperation(), got.order.GetVersion())
		}
	case <-time.After(time.Millisecond * 100):
		// Good: B is correctly queued behind the post-round coalesce timer.
	}

	// Stream C: a second late arrival. Joins B in waitingToDisseminate
	// because the timer is still pending.
	cs, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, cs.Send(&v1pb.Host{
		Name: "c", Port: 1003, Entities: []string{"actorC"}, Id: "c", Namespace: "default",
	}))

	// When the timer fires, B and C should both receive LOCK at the same
	// version. Different versions would mean they triggered independent
	// rounds — i.e. the post-round timer was never armed.
	var rb *v1pb.PlacementOrder
	select {
	case got := <-bRecv:
		require.NoError(t, got.err)
		rb = got.order
	case <-time.After(time.Second * 5):
		require.Fail(t, "stream B did not receive LOCK after coalesce window")
	}
	require.Equal(t, "lock", rb.GetOperation())

	rc, err := cs.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", rc.GetOperation())

	require.Greater(t, rb.GetVersion(), v1, "post-round coalesced round must be after round 1")
	assert.Equal(t, rb.GetVersion(), rc.GetVersion(),
		"streams B and C must land in the same coalesced round (got B=%d C=%d)",
		rb.GetVersion(), rc.GetVersion())
}
