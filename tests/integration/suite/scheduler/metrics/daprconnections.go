/*
Copyright 2024 The Dapr Authors
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

package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(daprconnections))
}

type daprconnections struct {
	scheduler *scheduler.Scheduler
	daprdA    *daprd.Daprd
	daprdB    *daprd.Daprd
	daprdC    *daprd.Daprd
}

func (c *daprconnections) Setup(t *testing.T) []framework.Option {
	c.scheduler = scheduler.New(t)
	srv := app.New(t)

	c.daprdA = daprd.New(t,
		daprd.WithSchedulerAddresses(c.scheduler.Address()),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
	)

	c.daprdB = daprd.New(t,
		daprd.WithSchedulerAddresses(c.scheduler.Address()),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
	)

	c.daprdC = daprd.New(t,
		daprd.WithSchedulerAddresses(c.scheduler.Address()),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
	)

	return []framework.Option{
		framework.WithProcesses(srv, c.scheduler),
	}
}

func (c *daprconnections) Run(t *testing.T, ctx context.Context) {
	c.scheduler.WaitUntilRunning(t, ctx)

	t.Run("ensure dapr connection with scheduler metric", func(t *testing.T) {
		// 0 sidecars connected
		metrics := c.scheduler.Metrics(t, ctx).All()
		assert.Equal(t, 0, int(metrics["dapr_scheduler_sidecars_connected"]))

		// 1 sidecar connected
		c.daprdA.Run(t, ctx)
		c.daprdA.WaitUntilRunning(t, ctx)
		t.Cleanup(func() { c.daprdA.Cleanup(t) })
		assert.EventuallyWithT(t, func(ct *assert.CollectT) {
			metrics = c.scheduler.Metrics(ct, ctx).All()
			assert.Equal(ct, 1, int(metrics["dapr_scheduler_sidecars_connected"]))
		}, 15*time.Second, 10*time.Millisecond, "daprdA sidecar didn't connect to Scheduler in time")

		// 2 sidecars connected
		c.daprdB.Run(t, ctx)
		c.daprdB.WaitUntilRunning(t, ctx)
		t.Cleanup(func() { c.daprdB.Cleanup(t) })
		assert.EventuallyWithT(t, func(ct *assert.CollectT) {
			metrics = c.scheduler.Metrics(ct, ctx).All()
			assert.Equal(ct, 2, int(metrics["dapr_scheduler_sidecars_connected"]))
		}, 15*time.Second, 10*time.Millisecond, "daprdB sidecar didn't connect to Scheduler in time")

		// 3 sidecars connected
		c.daprdC.Run(t, ctx)
		c.daprdC.WaitUntilRunning(t, ctx)
		t.Cleanup(func() { c.daprdC.Cleanup(t) })
		assert.EventuallyWithT(t, func(ct *assert.CollectT) {
			metrics = c.scheduler.Metrics(ct, ctx).All()
			assert.Equal(ct, 3, int(metrics["dapr_scheduler_sidecars_connected"]))
		}, 15*time.Second, 10*time.Millisecond, "daprdC sidecar didn't connect to Scheduler in time")

		// 2 sidecars connected
		c.daprdA.Cleanup(t)
		assert.EventuallyWithT(t, func(ct *assert.CollectT) {
			metrics = c.scheduler.Metrics(ct, ctx).All()
			assert.Equal(ct, 2, int(metrics["dapr_scheduler_sidecars_connected"]))
		}, 15*time.Second, 10*time.Millisecond, "daprdA sidecar didn't disconnect from Scheduler in time")

		// 1 sidecar connected
		c.daprdB.Cleanup(t)
		assert.EventuallyWithT(t, func(ct *assert.CollectT) {
			metrics = c.scheduler.Metrics(ct, ctx).All()
			assert.Equal(ct, 1, int(metrics["dapr_scheduler_sidecars_connected"]))
		}, 15*time.Second, 10*time.Millisecond, "daprdB sidecar didn't disconnect from Scheduler in time")

		// 0 sidecars connected
		c.daprdC.Cleanup(t)
		assert.EventuallyWithT(t, func(ct *assert.CollectT) {
			metrics = c.scheduler.Metrics(t, ctx).All()
			assert.Equal(ct, 0, int(metrics["dapr_scheduler_sidecars_connected"]))
		}, 15*time.Second, 10*time.Millisecond, "daprdC sidecar didn't disconnect from Scheduler in time")
	})
}
