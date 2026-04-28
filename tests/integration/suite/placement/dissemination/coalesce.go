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
	suite.Register(new(coalesce))
}

// coalesce verifies the post-round coalesce window: when --disseminate-coalesce-window
// is set and waitingToDisseminate is non-empty at the moment a round
// transitions back to REPORT, the disseminator defers the next round by
// the configured window so additional new connections arriving inside
// the window fold into a single batched round instead of triggering
// independent rounds.
type coalesce struct {
	place *placement.Placement
}

func (c *coalesce) Setup(t *testing.T) []framework.Option {
	c.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*10),
		placement.WithDisseminateCoalesceWindow(time.Millisecond*500),
	)

	return []framework.Option{
		framework.WithProcesses(c.place),
	}
}

func (c *coalesce) Run(t *testing.T, ctx context.Context) {
	c.place.WaitUntilRunning(t, ctx)

	assert.Eventually(t, func() bool {
		return c.place.IsLeader(t, ctx)
	}, time.Second*10, time.Millisecond*10)

	client := c.place.Client(t, ctx)

	// Stream A: triggers and drives round 1.
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

	// While round 1 is between UPDATE and UNLOCK on the server side,
	// connect stream B with a different actor type. It cannot start its
	// own round (currentOperation != REPORT) so it queues in
	// waitingToDisseminate. When stream A acks UNLOCK below, the server
	// transitions to REPORT and the coalesce timer arms because the
	// queue is non-empty.
	b, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, b.Send(&v1pb.Host{
		Name: "b", Port: 1002, Entities: []string{"actorB"}, Id: "b", Namespace: "default",
	}))

	require.NoError(t, a.Send(&v1pb.Host{Name: "a", Port: 1001, Entities: []string{"actorA"}, Id: "a", Namespace: "default"}))

	// UNLOCK v1.
	r, err = a.Recv()
	require.NoError(t, err)
	require.Equal(t, "unlock", r.GetOperation())
	v1 := r.GetVersion()

	// Send UNLOCK ack. After the server processes this, the disseminator
	// transitions to REPORT, sees waitingToDisseminate has stream B, and
	// arms the coalesce timer (does NOT immediately start the next round).
	require.NoError(t, a.Send(&v1pb.Host{Name: "a", Port: 1001, Entities: []string{"actorA"}, Id: "a", Namespace: "default"}))

	// Sleep for half the coalesce window so we know the timer is armed,
	// then connect a third stream C. Because the timer is armed,
	// handleAdd routes C into waitingToDisseminate instead of starting
	// its own round. When the timer fires (about 250ms later from now),
	// the disseminator processes B and C together in ONE round.
	time.Sleep(time.Millisecond * 250)

	cs, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, cs.Send(&v1pb.Host{
		Name: "c", Port: 1003, Entities: []string{"actorC"}, Id: "c", Namespace: "default",
	}))

	// B and C should both receive LOCK at the SAME version (one batched
	// round). Without coalescing, B's connection would have triggered
	// round 2 immediately at unlock-ack of A, and C's connection 250ms
	// later would have triggered round 3, landing on different
	// versions.
	rb, err := b.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", rb.GetOperation())

	rc, err := cs.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", rc.GetOperation())

	require.Greater(t, rb.GetVersion(), v1, "batched round must be after round 1")
	assert.Equal(t, rb.GetVersion(), rc.GetVersion(),
		"streams B and C must land in the same coalesced round (got B=%d C=%d)",
		rb.GetVersion(), rc.GetVersion())
}
