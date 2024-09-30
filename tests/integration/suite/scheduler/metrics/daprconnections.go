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

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(daprconnections))
}

type daprconnections struct {
	scheduler *scheduler.Scheduler
	daprd     *daprd.Daprd

	connectionlogline *logline.LogLine
}

func (c *daprconnections) Setup(t *testing.T) []framework.Option {
	c.connectionlogline = logline.New(t,
		logline.WithStdoutLineContains(
			`Adding a Sidecar connection to Scheduler for appID: default/C`,
		),
	)
	c.scheduler = scheduler.New(t,
		scheduler.WithLogLevel("debug"),
		scheduler.WithExecOptions(
			exec.WithStdout(c.connectionlogline.Stdout()),
		),
	)
	srv := app.New(t)
	c.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(c.scheduler.Address()),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppID("C"),
	)

	return []framework.Option{
		framework.WithProcesses(c.connectionlogline, srv, c.scheduler, c.daprd),
	}
}

func (c *daprconnections) Run(t *testing.T, ctx context.Context) {
	c.scheduler.WaitUntilRunning(t, ctx)
	c.daprd.WaitUntilRunning(t, ctx)

	t.Run("ensure dapr connection with scheduler metric", func(t *testing.T) {
		metrics := c.scheduler.Metrics(t, ctx)
		assert.Equal(t, 1, int(metrics["dapr_scheduler_sidecars_connected_total"]))
	})

	c.connectionlogline.EventuallyFoundAll(t)
}
