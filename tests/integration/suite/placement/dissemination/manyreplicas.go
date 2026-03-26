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
	"sync"
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
	suite.Register(new(manyreplicas))
}

type manyreplicas struct {
	place *placement.Placement
}

func (m *manyreplicas) Setup(t *testing.T) []framework.Option {
	m.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*5),
	)

	return []framework.Option{
		framework.WithProcesses(m.place),
	}
}

func (m *manyreplicas) Run(t *testing.T, ctx context.Context) {
	m.place.WaitUntilRunning(t, ctx)

	assert.Eventually(t, func() bool {
		return m.place.IsLeader(t, ctx)
	}, time.Second*10, time.Millisecond*10)

	client := m.place.Client(t, ctx)

	const numReplicas = 15

	streams := make([]v1pb.Placement_ReportDaprStatusClient, numReplicas)
	for i := range numReplicas {
		s, err := client.ReportDaprStatus(ctx)
		require.NoError(t, err)
		id := "replica-" + strconv.Itoa(i)
		require.NoError(t, s.Send(&v1pb.Host{
			Name:      id,
			Port:      int64(3000 + i),
			Entities:  []string{"myactor"},
			Id:        id,
			Namespace: "default",
		}))
		streams[i] = s
	}

	var wg sync.WaitGroup
	for i, s := range streams {
		wg.Go(func() {
			id := "replica-" + strconv.Itoa(i)
			for {
				resp, err := s.Recv()
				if err != nil {
					return
				}
				op := resp.GetOperation()
				if op == "lock" || op == "update" || op == "unlock" {
					_ = s.Send(&v1pb.Host{
						Name: id, Port: int64(3000 + i),
						Entities:  []string{"myactor"},
						Id:        id,
						Namespace: "default",
						Operation: hostOpFromString(op),
						Version:   &resp.Version,
					})
				}
			}
		})
	}

	// All 15 replicas should appear in the placement table.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := m.place.PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		assert.Len(c, table.Tables["default"].Hosts, numReplicas)
	}, time.Second*30, time.Millisecond*10)

	// Close all streams gracefully.
	for _, s := range streams {
		require.NoError(t, s.CloseSend())
	}
	wg.Wait()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := m.place.PlacementTables(t, ctx)
		if table.Tables["default"] == nil {
			return
		}
		assert.Empty(c, table.Tables["default"].Hosts)
	}, time.Second*15, time.Millisecond*10)
}
