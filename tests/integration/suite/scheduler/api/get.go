/*
Copyright 2025 The Dapr Authors
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

package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(get))
}

type get struct {
	scheduler *scheduler.Scheduler
}

func (g *get) Setup(t *testing.T) []framework.Option {
	g.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(g.scheduler),
	}
}

func (g *get) Run(t *testing.T, ctx context.Context) {
	g.scheduler.WaitUntilRunning(t, ctx)

	client := g.scheduler.Client(t, ctx)

	_, err := client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name: "test",
		Job:  &schedulerv1pb.Job{Schedule: ptr.Of("@daily")},
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     "foo",
			Namespace: "default",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{Job: new(schedulerv1pb.TargetJob)},
			},
		},
	})
	require.NoError(t, err)
	_, err = client.GetJob(ctx, &schedulerv1pb.GetJobRequest{
		Name: "test",
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     "foo",
			Namespace: "default",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{Job: new(schedulerv1pb.TargetJob)},
			},
		},
	})
	require.NoError(t, err)
}
