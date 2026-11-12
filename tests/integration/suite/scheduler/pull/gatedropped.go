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
	suite.Register(new(gatedropped))
}

// gatedropped asserts a concurrency limit that no connected stream sets any
// more, kept only for a held pull trigger, ends when that trigger resolves.
type gatedropped struct {
	scheduler *scheduler.Scheduler
}

func (s *gatedropped) Setup(t *testing.T) []framework.Option {
	s.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(s.scheduler),
	}
}

func (s *gatedropped) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)
	client := s.scheduler.Client(t, ctx)
	schedule := func(name, actorID string) {
		t.Helper()
		_, err := client.ScheduleJob(ctx, s.scheduler.JobNowPull(name, namespace, appID, actorType, actorID))
		require.NoError(t, err)
	}

	limited := sidecar("addr-1", free(3))
	limited.ConcurrencyLimits = []*schedulerv1.ConcurrencyLimit{{
		Target:        &schedulerv1.ConcurrencyLimit_Actor{Actor: &schedulerv1.ConcurrencyLimitActor{Type: actorType}},
		MaxConcurrent: 1,
	}}
	stream := s.scheduler.WatchJobsStream(t, ctx, limited)
	s.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)
	schedule("first", "instance1::0")
	require.Equal(t, "first", stream.Recv().GetName())

	// The sidecar reconnects without the limit.
	stream.Close()
	s.scheduler.WaitUntilSidecarsConnected(t, ctx, 0)
	first := reminder("instance1::0", "first")
	stream = s.scheduler.WatchJobsStream(t, ctx, sidecar("addr-1", free(2), first))
	s.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)

	schedule("second", "instance2::0")
	stream.NoRecv(time.Second)
	stream.ResultFor(first, schedulerv1.WatchJobsRequestResultStatus_SUCCESS)
	require.Equal(t, "second", stream.Recv().GetName())

	schedule("third", "instance3::0")
	assert.Equal(t, "third", stream.Recv().GetName(), "the limit ends with the trigger that held it")
}
