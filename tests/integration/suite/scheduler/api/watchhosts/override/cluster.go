/*
Copyright 2025 The Dapr Authors
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

package override

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	schedulercluster "github.com/dapr/dapr/tests/integration/framework/process/scheduler/cluster"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(cluster))
}

type cluster struct {
	schedulers *schedulercluster.Cluster
}

func (c *cluster) Setup(t *testing.T) []framework.Option {
	c.schedulers = schedulercluster.New(t,
		schedulercluster.WithCount(3),
		schedulercluster.WithOverrideBroadcastHostPorts(
			"1.2.3.4:5000",
			"2.3.4.5:5001",
			"3.4.5.6:5002",
		),
	)

	return []framework.Option{
		framework.WithProcesses(c.schedulers),
	}
}

func (c *cluster) Run(t *testing.T, ctx context.Context) {
	c.schedulers.WaitUntilRunning(t, ctx)

	stream, err := c.schedulers.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		resp, err := stream.Recv()
		require.NoError(t, err)
		got := make([]string, 0, len(resp.GetHosts()))
		for _, host := range resp.GetHosts() {
			got = append(got, host.GetAddress())
		}
		assert.ElementsMatch(col, []string{
			"1.2.3.4:5000",
			"2.3.4.5:5001",
			"3.4.5.6:5002",
		}, got)
	}, time.Second*10, time.Millisecond*10)
}
