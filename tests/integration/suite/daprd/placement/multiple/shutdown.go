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

package multiple

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(shutdown))
}

type shutdown struct {
	daprds []*daprd.Daprd
	place  *placement.Placement
}

func (s *shutdown) Setup(t *testing.T) []framework.Option {
	s.place = placement.New(t)
	scheduler := scheduler.New(t)

	appID, err := uuid.NewUUID()
	require.NoError(t, err)

	s.daprds = make([]*daprd.Daprd, 3)
	for i := range 3 {
		s.daprds[i] = daprd.New(t,
			daprd.WithPlacementAddresses(s.place.Address()),
			daprd.WithInMemoryActorStateStore("foo"),
			daprd.WithScheduler(scheduler),
			daprd.WithAppID(appID.String()),
		)
	}

	procs := []process.Interface{
		s.place,
		scheduler,
	}
	for _, d := range s.daprds {
		procs = append(procs, d)
	}

	return []framework.Option{
		framework.WithProcesses(procs...),
	}
}

func (s *shutdown) Run(t *testing.T, ctx context.Context) {
	for _, s := range s.daprds {
		s.WaitUntilRunning(t, ctx)
	}

	hosts := make([]placement.Host, 0, len(s.daprds))
	for _, s := range s.daprds {
		hosts = append(hosts, placement.Host{
			Name:      s.InternalGRPCAddress(),
			ID:        s.AppID(),
			APIVLevel: 20,
			Namespace: "default",
		})
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := s.place.PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		assert.ElementsMatch(c, hosts, table.Tables["default"].Hosts)
		assert.Equal(c, uint64(3), table.Tables["default"].Version)
	}, time.Second*10, time.Millisecond*10)

	for i := range s.daprds[:2] {
		s.daprds[i].Cleanup(t)
		hosts = hosts[1:]
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			table := s.place.PlacementTables(t, ctx)
			if !assert.NotNil(c, table.Tables["default"]) {
				return
			}
			assert.ElementsMatch(c, hosts, table.Tables["default"].Hosts)
		}, time.Second*20, time.Millisecond*10)
	}

	s.daprds[2].Cleanup(t)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := s.place.PlacementTables(t, ctx)
		assert.Equal(c, &placement.TableState{
			Tables: make(map[string]*placement.Table),
		}, table)
	}, time.Second*10, time.Millisecond*10)
}
