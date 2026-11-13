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
	"strconv"
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
	suite.Register(new(weight))
}

// weight asserts two names at the same level share deliveries by fairness
// weight: at 1.5 : 0.5 the heavy name gets 6 of the first 8 slots.
type weight struct {
	scheduler *scheduler.Scheduler
}

func (w *weight) Setup(t *testing.T) []framework.Option {
	w.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(w.scheduler),
	}
}

func (w *weight) Run(t *testing.T, ctx context.Context) {
	w.scheduler.WaitUntilRunning(t, ctx)
	client := w.scheduler.Client(t, ctx)

	stream := w.scheduler.WatchJobsStream(t, ctx, sidecar("127.0.0.1:1000", free(0)))
	w.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)

	const each = 8
	for i := range each {
		for name, fairness := range map[string]float32{"heavy": 1.5, "light": 0.5} {
			job := name + "-" + strconv.Itoa(i)
			req := w.scheduler.JobNowPull(job, namespace, appID, actorType, job+"::0")
			req.Metadata.ConcurrencyKey = new(name)
			req.Metadata.FairnessWeight = new(fairness)
			_, err := client.ScheduleJob(ctx, req)
			require.NoError(t, err)
		}
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, float64(2*each), w.scheduler.Metrics(t, ctx).SumWithLabels("dapr_scheduler_workflow_activity_backlog", "app_id:"+appID), 0)
	}, time.Second*20, time.Millisecond*10)

	counts := map[string]int{}
	for range each {
		stream.PullSlots(actorType, 1)
		resp := stream.Recv()
		counts[resp.GetMetadata().GetConcurrencyKey()]++
		stream.Result(resp.GetId(), schedulerv1.WatchJobsRequestResultStatus_SUCCESS)
	}
	assert.Equal(t, map[string]int{"heavy": 6, "light": 2}, counts)

	for range each {
		stream.PullSlots(actorType, 1)
		resp := stream.Recv()
		counts[resp.GetMetadata().GetConcurrencyKey()]++
		stream.Result(resp.GetId(), schedulerv1.WatchJobsRequestResultStatus_SUCCESS)
	}
	assert.Equal(t, map[string]int{"heavy": each, "light": each}, counts, "every trigger is delivered once")
	stream.NoRecv(time.Second)
}
