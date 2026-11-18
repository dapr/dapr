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
	suite.Register(new(relist))
}

// relist asserts the latest listing of a sidecar decides whether it still
// runs a pull trigger: a trigger one new stream listed and a later one did
// not, before the stream that delivered it closed, is delivered again after
// the reattach window even though the sidecar stays connected.
type relist struct {
	scheduler *scheduler.Scheduler
}

func (s *relist) Setup(t *testing.T) []framework.Option {
	s.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(s.scheduler),
	}
}

func (s *relist) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)
	client := s.scheduler.Client(t, ctx)
	schedule := func(name, actorID string) {
		t.Helper()
		_, err := client.ScheduleJob(ctx, s.scheduler.JobNowPull(name, namespace, appID, actorType, actorID))
		require.NoError(t, err)
	}

	first := s.scheduler.WatchJobsStream(t, ctx, sidecar("addr-1", free(1)))
	s.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)
	schedule("pull-1", "instance1::0")
	require.Equal(t, "pull-1", first.Recv().GetName())

	s.scheduler.WatchJobsStream(t, ctx, sidecar("addr-1", free(0), reminder("instance1::0", "pull-1")))
	s.scheduler.WaitUntilSidecarsConnected(t, ctx, 2)
	s.scheduler.WatchJobsStream(t, ctx, sidecar("addr-1", free(0)))
	s.scheduler.WaitUntilSidecarsConnected(t, ctx, 3)
	first.Close()
	s.scheduler.WaitUntilSidecarsConnected(t, ctx, 2)
	closed := time.Now()

	spare := s.scheduler.WatchJobsStream(t, ctx, sidecar("addr-2", free(1)))
	s.scheduler.WaitUntilSidecarsConnected(t, ctx, 3)
	spare.NoRecv(time.Until(closed.Add(time.Second * 25)))
	assert.Equal(t, "pull-1", spare.Recv().GetName(), "the sidecar's latest listing no longer has it")
}
