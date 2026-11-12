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

package pull

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(priority))
}

// priority asserts parked pull triggers are delivered by level, highest
// first, and that no level means normal.
type priority struct {
	scheduler *scheduler.Scheduler
}

func (p *priority) Setup(t *testing.T) []framework.Option {
	p.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(p.scheduler),
	}
}

func (p *priority) Run(t *testing.T, ctx context.Context) {
	p.scheduler.WaitUntilRunning(t, ctx)
	client := p.scheduler.Client(t, ctx)

	stream := p.scheduler.WatchJobsStream(t, ctx, sidecar("127.0.0.1:1000", free(0)))
	p.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)

	schedule := func(name string, level schedulerv1.PriorityLevel) {
		req := p.scheduler.JobNowPull(name, namespace, appID, actorType, name+"::0")
		req.Metadata.ConcurrencyKey = new("act")
		if level != schedulerv1.PriorityLevel_PRIORITY_LEVEL_UNSPECIFIED {
			req.Metadata.PriorityLevel = level.Enum()
		}
		_, err := client.ScheduleJob(ctx, req)
		require.NoError(t, err)
	}
	schedule("background", schedulerv1.PriorityLevel_PRIORITY_LEVEL_BACKGROUND)
	schedule("low", schedulerv1.PriorityLevel_PRIORITY_LEVEL_LOW)
	schedule("unset", schedulerv1.PriorityLevel_PRIORITY_LEVEL_UNSPECIFIED)
	schedule("high", schedulerv1.PriorityLevel_PRIORITY_LEVEL_HIGH)
	schedule("critical", schedulerv1.PriorityLevel_PRIORITY_LEVEL_CRITICAL)

	stream.NoRecv(time.Second * 2)

	for _, want := range []string{"critical", "high", "unset", "low", "background"} {
		stream.PullSlots(actorType, 1)
		resp := stream.Recv()
		require.Equal(t, want, resp.GetName())
		assert.True(t, resp.GetPull())
		stream.Result(resp.GetId(), schedulerv1.WatchJobsRequestResultStatus_SUCCESS)
	}
	stream.NoRecv(time.Second)
}
