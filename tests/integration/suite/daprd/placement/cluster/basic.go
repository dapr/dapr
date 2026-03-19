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

package cluster

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
	"github.com/dapr/dapr/tests/integration/framework/process/placement/cluster"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	daprds []*daprd.Daprd
	place  *cluster.Cluster
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.place = cluster.New(t)
	scheduler := scheduler.New(t)

	appID, err := uuid.NewUUID()
	require.NoError(t, err)

	b.daprds = make([]*daprd.Daprd, 3)
	for i := range 3 {
		b.daprds[i] = daprd.New(t,
			daprd.WithPlacementAddresses(b.place.Addresses()...),
			daprd.WithInMemoryActorStateStore("foo"),
			daprd.WithScheduler(scheduler),
			daprd.WithAppID(appID.String()),
		)
	}

	procs := make([]process.Interface, 0, 2+len(b.daprds))
	procs = append(procs,
		b.place,
		scheduler,
	)
	for _, d := range b.daprds {
		procs = append(procs, d)
	}

	return []framework.Option{
		framework.WithProcesses(procs...),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	for _, d := range b.daprds {
		d.WaitUntilRunning(t, ctx)
	}

	leader := b.place.Leader(t, ctx)

	// Daprds in this test have no actor types, so the placement table should
	// have no hosts.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := leader.PlacementTables(t, ctx)
		if !assert.Contains(c, table.Tables, "default") {
			return
		}
		assert.Nil(c, table.Tables["default"].Hosts)
	}, time.Second*30, time.Millisecond*10)
}
