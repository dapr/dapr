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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(expire))
}

// expire asserts a pull trigger whose stream closed is delivered again once
// the 30 second reattach window passes without its sidecar listing it as
// running: one sidecar never comes back, the other comes back without it.
type expire struct {
	scheduler *scheduler.Scheduler
}

func (e *expire) Setup(t *testing.T) []framework.Option {
	e.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(e.scheduler),
	}
}

func (e *expire) Run(t *testing.T, ctx context.Context) {
	e.scheduler.WaitUntilRunning(t, ctx)

	gone := e.scheduler.WatchJobsStream(t, ctx, sidecar("addr-1", free(1)))
	back := e.scheduler.WatchJobsStream(t, ctx, sidecar("addr-2", free(1)))
	e.scheduler.WaitUntilSidecarsConnected(t, ctx, 2)

	client := e.scheduler.Client(t, ctx)
	for _, name := range []string{"lost", "dropped"} {
		_, err := client.ScheduleJob(ctx, e.scheduler.JobNowPull(name, namespace, appID, actorType, name+"::0"))
		require.NoError(t, err)
	}
	gone.Recv()
	back.Recv()

	gone.Close()
	back.Close()
	closed := time.Now()
	e.scheduler.WaitUntilSidecarsConnected(t, ctx, 0)

	back = e.scheduler.WatchJobsStream(t, ctx, sidecar("addr-2", free(0)))
	spare := e.scheduler.WatchJobsStream(t, ctx, sidecar("addr-3", free(2)))
	e.scheduler.WaitUntilSidecarsConnected(t, ctx, 2)

	spare.NoRecv(time.Until(closed.Add(time.Second * 25)))
	assert.ElementsMatch(t, []string{"lost", "dropped"}, []string{spare.Recv().GetName(), spare.Recv().GetName()})
	back.NoRecv(0)
}
