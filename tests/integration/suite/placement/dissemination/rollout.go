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
	"strconv"
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
	suite.Register(new(rollout))
}

type rollout struct {
	place *placement.Placement
}

func (r *rollout) Setup(t *testing.T) []framework.Option {
	r.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second),
	)

	return []framework.Option{
		framework.WithProcesses(r.place),
	}
}

func (r *rollout) Run(t *testing.T, ctx context.Context) {
	r.place.WaitUntilRunning(t, ctx)

	assert.Eventually(t, func() bool {
		return r.place.IsLeader(t, ctx)
	}, time.Second*10, time.Millisecond*10)

	client := r.place.Client(t, ctx)

	// Phase 1: Establish a stable cluster of 5 "old" pods by adding them one at
	// a time, completing each dissemination round before adding the next. This
	// ensures a clean baseline before the rollout churn.
	const numPods = 5
	oldStreams := make([]v1pb.Placement_ReportDaprStatusClient, numPods)
	for i := range numPods {
		s, err := client.ReportDaprStatus(ctx)
		require.NoError(t, err)
		id := "old-" + strconv.Itoa(i)
		require.NoError(t, s.Send(&v1pb.Host{
			Name:      id,
			Port:      int64(2000 + i),
			Entities:  []string{"myactor"},
			Id:        id,
			Namespace: "default",
		}))
		oldStreams[i] = s

		allCurrent := oldStreams[:i+1]
		for j, cs := range allCurrent {
			resp, rerr := cs.Recv()
			require.NoError(t, rerr, "stream old-%d lock (round %d)", j, i)
			require.Equal(t, "lock", resp.GetOperation())
		}
		for j, cs := range allCurrent {
			cid := "old-" + strconv.Itoa(j)
			require.NoError(t, cs.Send(&v1pb.Host{
				Name: cid, Port: int64(2000 + j),
				Entities: []string{"myactor"}, Id: cid, Namespace: "default",
			}))
		}
		for j, cs := range allCurrent {
			resp, rerr := cs.Recv()
			require.NoError(t, rerr, "stream old-%d update (round %d)", j, i)
			require.Equal(t, "update", resp.GetOperation())
		}
		for j, cs := range allCurrent {
			cid := "old-" + strconv.Itoa(j)
			require.NoError(t, cs.Send(&v1pb.Host{
				Name: cid, Port: int64(2000 + j),
				Entities: []string{"myactor"}, Id: cid, Namespace: "default",
			}))
		}
		for j, cs := range allCurrent {
			resp, rerr := cs.Recv()
			require.NoError(t, rerr, "stream old-%d unlock (round %d)", j, i)
			require.Equal(t, "unlock", resp.GetOperation())
		}
		for j, cs := range allCurrent {
			cid := "old-" + strconv.Itoa(j)
			require.NoError(t, cs.Send(&v1pb.Host{
				Name: cid, Port: int64(2000 + j),
				Entities: []string{"myactor"}, Id: cid, Namespace: "default",
			}))
		}
	}

	// Phase 2: Simulate a rolling update. One by one, close an old stream and
	// open a new one. Each close and open changes the store, triggering a new
	// dissemination version.
	newStreams := make([]v1pb.Placement_ReportDaprStatusClient, numPods)
	for i := range numPods {
		require.NoError(t, oldStreams[i].CloseSend())

		s, err := client.ReportDaprStatus(ctx)
		require.NoError(t, err)
		id := "new-" + strconv.Itoa(i)
		require.NoError(t, s.Send(&v1pb.Host{
			Name:      id,
			Port:      int64(3000 + i),
			Entities:  []string{"myactor"},
			Id:        id,
			Namespace: "default",
		}))
		newStreams[i] = s
	}

	// Phase 3: Drive the new streams through dissemination. Each stream needs
	// to respond to LOCK→UPDATE→UNLOCK messages. Run a goroutine per stream
	// that auto-responds to dissemination messages.
	for i, s := range newStreams {
		go func() {
			id := "new-" + strconv.Itoa(i)
			for {
				resp, err := s.Recv()
				if err != nil {
					return
				}
				op := resp.GetOperation()
				if op == "lock" || op == "update" || op == "unlock" {
					_ = s.Send(&v1pb.Host{
						Name: id, Port: int64(3000 + i),
						Entities: []string{"myactor"}, Id: id, Namespace: "default",
						Operation: hostOpFromString(op),
						Version:   &resp.Version,
					})
				}
			}
		}()
	}

	// After driving the protocol, the placement tables should stabilize
	// with all 5 new pods.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := r.place.PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		hosts := table.Tables["default"].Hosts
		names := make([]string, len(hosts))
		for i, h := range hosts {
			names[i] = h.Name
		}
		t.Logf("Table hosts (%d): %v (version=%d)", len(names), names, table.Tables["default"].Version)
		if !assert.Len(c, names, numPods) {
			return
		}
		for i := range numPods {
			assert.Contains(c, names, "new-"+strconv.Itoa(i))
		}
	}, time.Second*30, time.Millisecond*500)
}

func hostOpFromString(op string) v1pb.HostOperation {
	switch op {
	case "lock":
		return v1pb.HostOperation_LOCK
	case "update":
		return v1pb.HostOperation_UPDATE
	case "unlock":
		return v1pb.HostOperation_UNLOCK
	default:
		return v1pb.HostOperation_REPORT
	}
}
