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

package v2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

type fakeChannel struct {
	schedulerv1pb.Scheduler_ReportActorTypesClient

	recvResp *schedulerv1pb.PlacementOrder
	recvErr  error
	sent     []*schedulerv1pb.ReportActorTypesRequest
	sendErr  error
	closed   bool
	closeErr error
}

func (f *fakeChannel) Recv() (*schedulerv1pb.PlacementOrder, error) {
	return f.recvResp, f.recvErr
}

func (f *fakeChannel) Send(req *schedulerv1pb.ReportActorTypesRequest) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, req)
	return nil
}

func (f *fakeChannel) CloseSend() error {
	f.closed = true
	return f.closeErr
}

func newTest(ch *fakeChannel) *v2 {
	return New(Options{Channel: ch}).(*v2)
}

func TestRecv(t *testing.T) {
	t.Parallel()

	t.Run("parses operations", func(t *testing.T) {
		t.Parallel()

		for name, test := range map[string]struct {
			op  schedulerv1pb.Operation
			exp loops.OrderOp
		}{
			"lock":   {op: schedulerv1pb.Operation_OPERATION_LOCK, exp: loops.OrderLock},
			"update": {op: schedulerv1pb.Operation_OPERATION_UPDATE, exp: loops.OrderUpdate},
			"unlock": {op: schedulerv1pb.Operation_OPERATION_UNLOCK, exp: loops.OrderUnlock},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				tr := newTest(&fakeChannel{recvResp: &schedulerv1pb.PlacementOrder{
					Operation: test.op,
					Seq:       11,
				}})
				order, err := tr.Recv()
				require.NoError(t, err)
				assert.Equal(t, test.exp, order.Op)
			})
		}
	})

	t.Run("seq becomes the order version", func(t *testing.T) {
		t.Parallel()
		tr := newTest(&fakeChannel{recvResp: &schedulerv1pb.PlacementOrder{
			Operation: schedulerv1pb.Operation_OPERATION_LOCK,
			Seq:       42,
		}})
		order, err := tr.Recv()
		require.NoError(t, err)
		assert.Equal(t, uint64(42), order.Version)
	})

	t.Run("orders are always partial on v2", func(t *testing.T) {
		t.Parallel()
		// Every v2 order is a partial update, including the startup snapshot:
		// the receiver merges by actor type rather than replacing the whole
		// table, which is what keeps unaffected types serving.
		for _, op := range []schedulerv1pb.Operation{
			schedulerv1pb.Operation_OPERATION_LOCK,
			schedulerv1pb.Operation_OPERATION_UPDATE,
			schedulerv1pb.Operation_OPERATION_UNLOCK,
		} {
			tr := newTest(&fakeChannel{recvResp: &schedulerv1pb.PlacementOrder{Operation: op}})
			order, err := tr.Recv()
			require.NoError(t, err)
			assert.True(t, order.Partial)
		}
	})

	t.Run("lock carries the actor type scope", func(t *testing.T) {
		t.Parallel()
		tr := newTest(&fakeChannel{recvResp: &schedulerv1pb.PlacementOrder{
			Operation:  schedulerv1pb.Operation_OPERATION_LOCK,
			Seq:        3,
			ActorTypes: []string{"t1", "t2"},
		}})
		order, err := tr.Recv()
		require.NoError(t, err)
		assert.Equal(t, []string{"t1", "t2"}, order.Scope)
	})

	t.Run("empty scope means all types", func(t *testing.T) {
		t.Parallel()
		// An empty actor_types on LOCK is the startup snapshot covering every
		// type, not a lock over nothing.
		tr := newTest(&fakeChannel{recvResp: &schedulerv1pb.PlacementOrder{
			Operation: schedulerv1pb.Operation_OPERATION_LOCK,
			Seq:       1,
		}})
		order, err := tr.Recv()
		require.NoError(t, err)
		assert.Empty(t, order.Scope)
	})

	t.Run("update carries per type versions and tables", func(t *testing.T) {
		t.Parallel()

		tables := &schedulerv1pb.PlacementTables{
			HashAlgorithm: schedulerv1pb.HashAlgorithm_HASH_ALGORITHM_RENDEZVOUS,
			Entries: map[string]*schedulerv1pb.PlacementTable{
				"t1": {
					Hosts: map[string]*schedulerv1pb.PlacementHost{
						"10.0.0.1:50002": {Address: "10.0.0.1:50002", AppId: "app-1"},
					},
				},
			},
		}

		tr := newTest(&fakeChannel{recvResp: &schedulerv1pb.PlacementOrder{
			Operation:  schedulerv1pb.Operation_OPERATION_UPDATE,
			Seq:        7,
			ActorTypes: []string{"t1"},
			Versions:   map[string]uint64{"t1": 9},
			Tables:     tables,
		}})

		order, err := tr.Recv()
		require.NoError(t, err)
		assert.Equal(t, loops.OrderUpdate, order.Op)
		assert.Equal(t, uint64(7), order.Version)
		assert.Equal(t, map[string]uint64{"t1": 9}, order.Versions)
		require.NotNil(t, order.V2Tables)
		assert.Equal(t,
			schedulerv1pb.HashAlgorithm_HASH_ALGORITHM_RENDEZVOUS,
			order.V2Tables.GetHashAlgorithm(),
		)
		assert.Contains(t, order.V2Tables.GetEntries(), "t1")
	})

	t.Run("unknown operation errors", func(t *testing.T) {
		t.Parallel()
		tr := newTest(&fakeChannel{recvResp: &schedulerv1pb.PlacementOrder{
			Operation: schedulerv1pb.Operation_OPERATION_UNKNOWN,
		}})
		_, err := tr.Recv()
		require.ErrorContains(t, err, "unknown operation")
	})

	t.Run("out of range operation errors", func(t *testing.T) {
		t.Parallel()
		tr := newTest(&fakeChannel{recvResp: &schedulerv1pb.PlacementOrder{
			Operation: schedulerv1pb.Operation(99),
		}})
		_, err := tr.Recv()
		require.ErrorContains(t, err, "unknown operation")
	})

	t.Run("recv error is returned", func(t *testing.T) {
		t.Parallel()
		tr := newTest(&fakeChannel{recvErr: assert.AnError})
		_, err := tr.Recv()
		require.ErrorIs(t, err, assert.AnError)
	})
}

func TestSendReport(t *testing.T) {
	t.Parallel()

	t.Run("maps every reported field", func(t *testing.T) {
		t.Parallel()

		ch := new(fakeChannel)
		tr := newTest(ch)
		require.NoError(t, tr.SendReport(&loops.Report{
			Address:    "10.0.0.1:50002",
			AppID:      "app-1",
			Namespace:  "ns-1",
			ActorTypes: []string{"t1", "t2"},
		}))

		require.Len(t, ch.sent, 1)
		report := ch.sent[0].GetReport()
		require.NotNil(t, report)
		assert.Nil(t, ch.sent[0].GetAck())
		assert.Equal(t, "10.0.0.1:50002", report.GetAddress())
		assert.Equal(t, "app-1", report.GetAppId())
		assert.Equal(t, "ns-1", report.GetNamespace())
		assert.Equal(t, []string{"t1", "t2"}, report.GetActorTypes())
	})

	t.Run("a host with no actor types is still reported", func(t *testing.T) {
		t.Parallel()
		// A sidecar which only looks actors up, hosting none, must still
		// report so it receives placement tables.
		ch := new(fakeChannel)
		tr := newTest(ch)
		require.NoError(t, tr.SendReport(&loops.Report{
			Address:   "10.0.0.1:50002",
			AppID:     "app-1",
			Namespace: "ns-1",
		}))

		require.Len(t, ch.sent, 1)
		report := ch.sent[0].GetReport()
		require.NotNil(t, report)
		assert.Empty(t, report.GetActorTypes())
	})

	t.Run("send error is returned", func(t *testing.T) {
		t.Parallel()
		tr := newTest(&fakeChannel{sendErr: assert.AnError})
		require.ErrorIs(t, tr.SendReport(&loops.Report{}), assert.AnError)
	})
}

func TestSendAck(t *testing.T) {
	t.Parallel()

	t.Run("acks echo the operation and seq", func(t *testing.T) {
		t.Parallel()

		ch := new(fakeChannel)
		tr := newTest(ch)

		for op, exp := range map[loops.OrderOp]schedulerv1pb.Operation{
			loops.OrderLock:   schedulerv1pb.Operation_OPERATION_LOCK,
			loops.OrderUpdate: schedulerv1pb.Operation_OPERATION_UPDATE,
			loops.OrderUnlock: schedulerv1pb.Operation_OPERATION_UNLOCK,
		} {
			require.NoError(t, tr.SendAck(&loops.Ack{Op: op, Version: 13}))
			ack := ch.sent[len(ch.sent)-1].GetAck()
			require.NotNil(t, ack)
			assert.Nil(t, ch.sent[len(ch.sent)-1].GetReport())
			assert.Equal(t, exp, ack.GetOperation())
			// The ack must echo the round's seq, otherwise the leader cannot
			// match it to the in flight round.
			assert.Equal(t, uint64(13), ack.GetSeq())
		}
	})

	t.Run("unknown operation errors without sending", func(t *testing.T) {
		t.Parallel()
		ch := new(fakeChannel)
		tr := newTest(ch)
		require.ErrorContains(t, tr.SendAck(&loops.Ack{Op: loops.OrderOp(99)}), "unknown ack operation")
		assert.Empty(t, ch.sent)
	})

	t.Run("send error is returned", func(t *testing.T) {
		t.Parallel()
		tr := newTest(&fakeChannel{sendErr: assert.AnError})
		require.ErrorIs(t, tr.SendAck(&loops.Ack{Op: loops.OrderLock}), assert.AnError)
	})
}

func TestCloseSend(t *testing.T) {
	t.Parallel()

	t.Run("closes the underlying channel", func(t *testing.T) {
		t.Parallel()
		ch := new(fakeChannel)
		tr := newTest(ch)
		require.NoError(t, tr.CloseSend())
		assert.True(t, ch.closed)
	})

	t.Run("close error is returned", func(t *testing.T) {
		t.Parallel()
		tr := newTest(&fakeChannel{closeErr: assert.AnError})
		require.ErrorIs(t, tr.CloseSend(), assert.AnError)
	})
}
