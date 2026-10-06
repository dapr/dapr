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

package v1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

type fakeChannel struct {
	v1pb.Placement_ReportDaprStatusClient

	recvResp *v1pb.PlacementOrder
	recvErr  error
	sent     []*v1pb.Host
}

func (f *fakeChannel) Recv() (*v1pb.PlacementOrder, error) {
	return f.recvResp, f.recvErr
}

func (f *fakeChannel) Send(host *v1pb.Host) error {
	f.sent = append(f.sent, host)
	return nil
}

func newTest(ch *fakeChannel) *v1 {
	return New(Options{
		Channel:   ch,
		AppID:     "app-1",
		Namespace: "ns-1",
	}).(*v1)
}

func TestRecv(t *testing.T) {
	t.Parallel()

	t.Run("parses operations and version", func(t *testing.T) {
		t.Parallel()

		tests := map[string]struct {
			resp *v1pb.PlacementOrder
			exp  *loops.Order
		}{
			"lock": {
				resp: &v1pb.PlacementOrder{Operation: "lock", Version: 4},
				exp:  &loops.Order{Op: loops.OrderLock, Version: 4},
			},
			"unlock": {
				resp: &v1pb.PlacementOrder{Operation: "unlock", Version: 5},
				exp:  &loops.Order{Op: loops.OrderUnlock, Version: 5},
			},
			"update carries tables": {
				resp: &v1pb.PlacementOrder{
					Operation: "update",
					Version:   6,
					Tables:    &v1pb.PlacementTables{ReplicationFactor: 3},
				},
				exp: &loops.Order{
					Op:       loops.OrderUpdate,
					Version:  6,
					V1Tables: &v1pb.PlacementTables{ReplicationFactor: 3},
				},
			},
			"version falls back to tables version": {
				resp: &v1pb.PlacementOrder{
					Operation: "lock",
					Tables:    &v1pb.PlacementTables{Version: "7"},
				},
				exp: &loops.Order{
					Op:      loops.OrderLock,
					Version: 7,
				},
			},
		}

		for name, test := range tests {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				tr := newTest(&fakeChannel{recvResp: test.resp})
				order, err := tr.Recv()
				require.NoError(t, err)
				assert.Equal(t, test.exp.Op, order.Op)
				assert.Equal(t, test.exp.Version, order.Version)
				if test.exp.V1Tables != nil {
					assert.Equal(t, test.exp.V1Tables.GetReplicationFactor(), order.V1Tables.GetReplicationFactor())
				}
			})
		}
	})

	t.Run("unknown operation errors", func(t *testing.T) {
		t.Parallel()
		tr := newTest(&fakeChannel{recvResp: &v1pb.PlacementOrder{Operation: "invalid"}})
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

	ch := new(fakeChannel)
	tr := newTest(ch)
	require.NoError(t, tr.SendReport(&loops.Report{
		Address:    "10.0.0.1:50002",
		AppID:      "app-1",
		Namespace:  "ns-1",
		ActorTypes: []string{"t1", "t2"},
	}))

	require.Len(t, ch.sent, 1)
	host := ch.sent[0]
	assert.Equal(t, "10.0.0.1:50002", host.GetName())
	assert.Equal(t, "app-1", host.GetId())
	assert.Equal(t, "ns-1", host.GetNamespace())
	assert.Equal(t, []string{"t1", "t2"}, host.GetEntities())
	//nolint:staticcheck
	assert.Equal(t, uint32(20), host.GetApiLevel())
	assert.Equal(t, v1pb.HostOperation_REPORT, host.GetOperation())
	assert.Nil(t, host.Version)
}

func TestSendAck(t *testing.T) {
	t.Parallel()

	ch := new(fakeChannel)
	tr := newTest(ch)
	for op, exp := range map[loops.OrderOp]v1pb.HostOperation{
		loops.OrderLock:   v1pb.HostOperation_LOCK,
		loops.OrderUpdate: v1pb.HostOperation_UPDATE,
		loops.OrderUnlock: v1pb.HostOperation_UNLOCK,
	} {
		require.NoError(t, tr.SendAck(&loops.Ack{Op: op, Version: 9}))
		host := ch.sent[len(ch.sent)-1]
		assert.Equal(t, exp, host.GetOperation())
		assert.Equal(t, uint64(9), host.GetVersion())
		assert.Equal(t, "ns-1", host.GetNamespace())
		assert.Equal(t, "app-1", host.GetId())
	}

	require.Error(t, tr.SendAck(&loops.Ack{Op: loops.OrderOp(99)}))
}
