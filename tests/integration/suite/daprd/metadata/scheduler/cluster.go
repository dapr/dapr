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

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/cluster"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(three))
}

type three struct {
	cluster *cluster.Cluster
	daprd   *daprd.Daprd
}

func (h *three) Setup(t *testing.T) []framework.Option {
	h.cluster = cluster.New(t, cluster.WithCount(3))
	h.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(h.cluster.Addresses()[0]),
	)

	return []framework.Option{
		framework.WithProcesses(h.cluster, h.daprd),
	}
}

func (h *three) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)

	client := h.daprd.GRPCClient(t, ctx)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		assert.NoError(c, err)
		assert.ElementsMatch(c, h.cluster.Addresses(), resp.GetScheduler().GetConnectedAddresses())
	}, time.Second*10, time.Millisecond*10)

	sched := h.daprd.GetMetaScheduler(t, ctx)
	assert.NotNil(t, sched)
	assert.ElementsMatch(t, h.cluster.Addresses(), sched.GetConnectedAddresses())
}
