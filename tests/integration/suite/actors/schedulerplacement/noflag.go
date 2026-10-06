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

package schedulerplacement

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(noflag))
}

// noflag removes the placement service while the only scheduler runs without
// placement enabled: the scheduler must never advertise a placement leader,
// so nothing serves actors rather than an unflagged scheduler taking over.
type noflag struct {
	sched *scheduler.Scheduler
	place *placement.Placement
	daprd *daprd.Daprd
}

func (n *noflag) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/", func(w http.ResponseWriter, r *http.Request) {})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	n.sched = scheduler.New(t)
	n.place = placement.New(t)
	n.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithSchedulerAddresses(n.sched.Address()),
		daprd.WithPlacementAddresses(n.place.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(n.sched, n.place, srv, n.daprd),
	}
}

func (n *noflag) Run(t *testing.T, ctx context.Context) {
	n.sched.WaitUntilRunning(t, ctx)
	n.place.WaitUntilRunning(t, ctx)
	n.daprd.WaitUntilRunning(t, ctx)

	client := n.daprd.GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*15, time.Millisecond*10)

	n.place.Cleanup(t)

	// A scheduler without the flag never serves placement, whether or not a
	// placement service exists.
	require.Never(t, func() bool {
		stream, err := n.sched.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if err != nil {
			return false
		}
		//nolint:errcheck
		defer stream.CloseSend()
		resp, err := stream.Recv()
		if err != nil {
			return false
		}
		for _, host := range resp.GetHosts() {
			if host.GetLeader() {
				return true
			}
		}
		return false
	}, time.Second*15, time.Millisecond*10,
		"an unflagged scheduler must not advertise a placement leader")

	sctx, cancel := context.WithTimeout(ctx, time.Second*2)
	t.Cleanup(cancel)
	_, err := client.InvokeActor(sctx, &rtv1.InvokeActorRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Method:    "foo",
	})
	require.Error(t, err, "nothing serves actors with the placement service gone and the scheduler unflagged")
}
