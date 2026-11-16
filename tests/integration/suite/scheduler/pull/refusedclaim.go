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
	suite.Register(new(refusedclaim))
}

// refusedclaim asserts a trigger held for the sidecar that lists its reminder
// as running goes to another sidecar with a free slot once that sidecar
// refuses it with NO_CAPACITY.
type refusedclaim struct {
	scheduler *scheduler.Scheduler
}

func (s *refusedclaim) Setup(t *testing.T) []framework.Option {
	s.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(s.scheduler),
	}
}

func (s *refusedclaim) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)
	client := s.scheduler.Client(t, ctx)
	schedule := func(name, actorID string) {
		t.Helper()
		_, err := client.ScheduleJob(ctx, s.scheduler.JobNowPull(name, namespace, appID, actorType, actorID))
		require.NoError(t, err)
	}

	ref := reminder("instance1::0", "pull-1")
	claimant := s.scheduler.WatchJobsStream(t, ctx, sidecar("addr-1", nil, ref))
	other := s.scheduler.WatchJobsStream(t, ctx, sidecar("addr-2", free(1)))
	s.scheduler.WaitUntilSidecarsConnected(t, ctx, 2)

	schedule("pull-1", "instance1::0")
	other.NoRecv(time.Second)
	claimant.NoRecv(0)

	claimant.ResultFor(ref, schedulerv1.WatchJobsRequestResultStatus_NO_CAPACITY)
	assert.Equal(t, "pull-1", other.Recv().GetName())
}
