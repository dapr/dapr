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

package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(sidecarerrorstotal))
}

type sidecarerrorstotal struct {
	scheduler *scheduler.Scheduler
}

func (s *sidecarerrorstotal) Setup(t *testing.T) []framework.Option {
	s.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(s.scheduler),
	}
}

func (s *sidecarerrorstotal) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)

	client := s.scheduler.Client(t, ctx)

	for range 3 {
		_, err := client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
			Name: "test",
			Job:  &schedulerv1pb.Job{Schedule: new("@every 100s")},
			Metadata: &schedulerv1pb.JobMetadata{
				AppId:     "",
				Namespace: "",
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: new(schedulerv1pb.JobTargetMetadata_Job),
				},
			},
		})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	}

	for range 2 {
		_, err := client.DeleteJob(ctx, &schedulerv1pb.DeleteJobRequest{
			Name: "test",
			Metadata: &schedulerv1pb.JobMetadata{
				AppId:     "",
				Namespace: "",
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: new(schedulerv1pb.JobTargetMetadata_Job),
				},
			},
		})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := s.scheduler.MetricsWithLabels(t, ctx)
		total, ok := metrics.Metrics["dapr_scheduler_sidecar_errors_total"]
		if !assert.True(c, ok) {
			return
		}
		assert.InDelta(c, 5.0, total["reason=auth_failed"], 0.01)
	}, time.Second*15, time.Millisecond*10)
}
