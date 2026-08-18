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

package connections

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/placement/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/placement/loops/store"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/placement/loops/timeout"
	loopfake "github.com/dapr/kit/events/loop/fake"
)

type fakeStream struct {
	sent   []loops.EventStream
	closed bool
}

func newTest(t *testing.T) *connections {
	t.Helper()

	c := &connections{
		namespace:          "ns",
		disseminateTimeout: time.Second * 30,
		streams:            make(map[uint64]*streamConn),
		store:              store.New(),
		versions:           make(map[string]uint64),
		rounds:             make(map[uint64]*round),
		lockedTypes:        make(map[string]uint64),
		pendingTypes:       make(map[string]struct{}),
		oneshots:           make(map[uint64]uint64),
	}
	c.loop = loopfake.New[loops.EventConnections]()
	c.timeoutQ = timeout.New(timeout.Options{Loop: c.loop, Timeout: time.Second * 30})
	t.Cleanup(func() { c.timeoutQ.Close() })

	return c
}

func (c *connections) addFakeStream(idx uint64) *fakeStream {
	fs := new(fakeStream)
	c.streams[idx] = &streamConn{
		loop: loopfake.New[loops.EventStream]().
			WithEnqueue(func(e loops.EventStream) { fs.sent = append(fs.sent, e) }).
			WithClose(func(loops.EventStream) { fs.closed = true }),
	}
	if idx >= c.streamIDx {
		c.streamIDx = idx + 1
	}
	return fs
}

func (c *connections) report(idx uint64, types ...string) {
	c.handleReportedTypes(&loops.ReportedTypes{
		StreamIDx: idx,
		Host: &schedulerv1pb.ActorHost{
			Address:    "host-" + strconv.FormatUint(idx, 10) + ":1",
			AppId:      "app",
			Namespace:  "ns",
			ActorTypes: types,
		},
	})
}

func (c *connections) ack(idx, seq uint64, op schedulerv1pb.Operation) {
	c.handleAck(&loops.Ack{StreamIDx: idx, Namespace: "ns", Seq: seq, Operation: op})
}

func lastLock(t *testing.T, fs *fakeStream) *loops.SendLock {
	t.Helper()
	require.NotEmpty(t, fs.sent)
	lock, ok := fs.sent[len(fs.sent)-1].(*loops.SendLock)
	require.True(t, ok, "expected last sent event to be SendLock, got %T", fs.sent[len(fs.sent)-1])
	return lock
}

func TestRoundLifecycle(t *testing.T) {
	t.Parallel()

	c := newTest(t)
	fs0 := c.addFakeStream(0)
	fs1 := c.addFakeStream(1)

	c.report(0, "t1")

	// Both streams get a LOCK for t1.
	lock0 := lastLock(t, fs0)
	lock1 := lastLock(t, fs1)
	assert.Equal(t, []string{"t1"}, lock0.Types)
	assert.Equal(t, lock0.Seq, lock1.Seq)
	assert.Equal(t, uint64(1), c.versions["t1"])
	assert.Equal(t, lock0.Seq, c.lockedTypes["t1"])

	seq := lock0.Seq

	// LOCK acks advance to UPDATE with the t1 table.
	c.ack(0, seq, schedulerv1pb.Operation_OPERATION_LOCK)
	assert.Len(t, fs0.sent, 1, "no UPDATE until all members acked")
	c.ack(1, seq, schedulerv1pb.Operation_OPERATION_LOCK)

	update0, ok := fs0.sent[len(fs0.sent)-1].(*loops.SendUpdate)
	require.True(t, ok)
	assert.Equal(t, []string{"t1"}, update0.Types)
	assert.Equal(t, map[string]uint64{"t1": 1}, update0.Versions)
	require.Contains(t, update0.Tables.GetEntries(), "t1")
	assert.Len(t, update0.Tables.GetEntries()["t1"].GetHosts(), 1)

	// UPDATE acks advance to UNLOCK.
	c.ack(0, seq, schedulerv1pb.Operation_OPERATION_UPDATE)
	c.ack(1, seq, schedulerv1pb.Operation_OPERATION_UPDATE)
	_, ok = fs1.sent[len(fs1.sent)-1].(*loops.SendUnlock)
	require.True(t, ok)

	// UNLOCK acks complete the round.
	c.ack(0, seq, schedulerv1pb.Operation_OPERATION_UNLOCK)
	c.ack(1, seq, schedulerv1pb.Operation_OPERATION_UNLOCK)
	assert.Empty(t, c.rounds)
	assert.Empty(t, c.lockedTypes)
	assert.Empty(t, c.pendingTypes)
}

func TestDisjointRoundsRunConcurrently(t *testing.T) {
	t.Parallel()

	c := newTest(t)
	fs0 := c.addFakeStream(0)
	fs1 := c.addFakeStream(1)

	c.report(0, "t1")
	seq1 := lastLock(t, fs0).Seq

	// A t2 change while t1 is locked starts a second, concurrent round.
	c.report(1, "t2")
	seq2 := lastLock(t, fs1).Seq

	assert.NotEqual(t, seq1, seq2)
	assert.Len(t, c.rounds, 2)
	assert.Equal(t, seq1, c.lockedTypes["t1"])
	assert.Equal(t, seq2, c.lockedTypes["t2"])
}

func TestLockedTypeQueuesUntilRoundCompletes(t *testing.T) {
	t.Parallel()

	c := newTest(t)
	fs0 := c.addFakeStream(0)
	fs1 := c.addFakeStream(1)

	c.report(0, "t1")
	seq := lastLock(t, fs0).Seq

	// Another host starts hosting t1 while it is locked: queued, no new
	// round.
	c.report(1, "t1")
	assert.Len(t, c.rounds, 1)
	assert.Contains(t, c.pendingTypes, "t1")

	// Complete the round: a follow-up round for t1 starts automatically with
	// a bumped version.
	for _, op := range []schedulerv1pb.Operation{
		schedulerv1pb.Operation_OPERATION_LOCK,
		schedulerv1pb.Operation_OPERATION_UPDATE,
		schedulerv1pb.Operation_OPERATION_UNLOCK,
	} {
		c.ack(0, seq, op)
		c.ack(1, seq, op)
	}

	require.Len(t, c.rounds, 1)
	next := lastLock(t, fs1)
	assert.NotEqual(t, seq, next.Seq)
	assert.Equal(t, []string{"t1"}, next.Types)
	assert.Equal(t, uint64(2), c.versions["t1"])
	assert.Empty(t, c.pendingTypes)
}

func TestStreamCloseAdvancesRound(t *testing.T) {
	t.Parallel()

	c := newTest(t)
	fs0 := c.addFakeStream(0)
	fs1 := c.addFakeStream(1)

	c.report(0, "t1")
	seq := lastLock(t, fs0).Seq

	// Stream 1 acks the LOCK; stream 0 disappears. The round must advance to
	// UPDATE for the remaining member instead of stalling.
	c.ack(1, seq, schedulerv1pb.Operation_OPERATION_LOCK)
	c.handleCloseStream(&loops.ConnCloseStream{StreamIDx: 0, Namespace: "ns"})

	assert.True(t, fs0.closed)
	_, ok := fs1.sent[len(fs1.sent)-1].(*loops.SendUpdate)
	require.True(t, ok, "round should advance to UPDATE after closing the unacked member")

	// The departed stream hosted t1, so t1 is pending a follow-up round.
	assert.Contains(t, c.pendingTypes, "t1")
}

func TestTimeoutEvictsUnackedAndRestartsRound(t *testing.T) {
	t.Parallel()

	c := newTest(t)
	fs0 := c.addFakeStream(0)
	fs1 := c.addFakeStream(1)

	c.report(0, "t1")
	c.report(1, "t2")
	seqT1 := c.lockedTypes["t1"]
	seqT2 := c.lockedTypes["t2"]

	// Stream 0 acks the t1 round, stream 1 does not; its timeout fires.
	c.ack(0, seqT1, schedulerv1pb.Operation_OPERATION_LOCK)
	c.handleTimeout(&loops.RoundTimeout{Seq: seqT1})

	assert.True(t, fs1.closed, "unacked stream must be evicted")
	assert.False(t, fs0.closed)
	assert.NotContains(t, c.rounds, seqT1, "timed out round must be aborted")

	// The survivor holds the aborted round's LOCK: it must receive that
	// round's UNLOCK so it releases the lock and its round timer, before the
	// fresh round's LOCK arrives.
	var unlocked bool
	for _, ev := range fs0.sent {
		if u, ok := ev.(*loops.SendUnlock); ok && u.Seq == seqT1 {
			unlocked = true
			assert.Equal(t, []string{"t1"}, u.Types)
		}
	}
	assert.True(t, unlocked, "survivor must receive UNLOCK for the aborted round")

	// A fresh round covering t1 goes to the survivor. t2 stays locked by its
	// own still in-flight round, with the evicted host's departure pending.
	next := lastLock(t, fs0)
	assert.NotEqual(t, seqT1, next.Seq)
	assert.Equal(t, []string{"t1"}, next.Types)
	assert.Contains(t, c.rounds, seqT2)
	assert.Contains(t, c.pendingTypes, "t2")
}

func TestLastStreamCloseClearsPending(t *testing.T) {
	t.Parallel()

	c := newTest(t)
	c.addFakeStream(0)
	c.report(0, "t1")

	c.handleCloseStream(&loops.ConnCloseStream{StreamIDx: 0, Namespace: "ns"})
	assert.Empty(t, c.pendingTypes)
	assert.Empty(t, c.streams)
}

func TestOneshotAcksAreNotRoundAcks(t *testing.T) {
	t.Parallel()

	c := newTest(t)
	c.addFakeStream(0)
	c.sendSnapshot(0)

	require.Len(t, c.oneshots, 1)
	var seq uint64
	for s := range c.oneshots {
		seq = s
	}

	c.ack(0, seq, schedulerv1pb.Operation_OPERATION_LOCK)
	require.Len(t, c.oneshots, 1)
	c.ack(0, seq, schedulerv1pb.Operation_OPERATION_UNLOCK)
	assert.Empty(t, c.oneshots)
	assert.Empty(t, c.rounds)
}
