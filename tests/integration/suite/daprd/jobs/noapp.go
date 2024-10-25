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

package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(noapp))
}

type noapp struct {
	scheduler *scheduler.Scheduler
	daprd     *daprd.Daprd
}

func (n *noapp) Setup(t *testing.T) []framework.Option {
	n.scheduler = scheduler.New(t)
	n.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(n.scheduler.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(n.scheduler, n.daprd),
	}
}

func (n *noapp) Run(t *testing.T, ctx context.Context) {
	n.scheduler.WaitUntilRunning(t, ctx)
	n.daprd.WaitUntilRunning(t, ctx)

	client := n.daprd.GRPCClient(t, ctx)

	_, err := client.ScheduleJobAlpha1(ctx, &rtv1.ScheduleJobRequest{
		Job: &rtv1.Job{
			Name:    "helloworld",
			DueTime: ptr.Of("2s"),
		},
	})
	require.NoError(t, err)

	resp, err := client.GetJobAlpha1(ctx, &rtv1.GetJobRequest{
		Name: "helloworld",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "helloworld", resp.GetJob().GetName())

	time.Sleep(3 * time.Second)

	resp, err = client.GetJobAlpha1(ctx, &rtv1.GetJobRequest{
		Name: "helloworld",
	})
	require.NoError(t, err)
	assert.Equal(t, "helloworld", resp.GetJob().GetName())
}
