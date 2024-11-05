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

package list

import (
	"bytes"
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(size))
}

type size struct {
	scheduler *scheduler.Scheduler
}

func (s *size) Setup(t *testing.T) []framework.Option {
	s.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(s.scheduler),
	}
}

func (s *size) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)
	client := s.scheduler.Client(t, ctx)

	data, err := anypb.New(wrapperspb.Bytes(bytes.Repeat([]byte{0x01}, 2e+6)))
	require.NoError(t, err)

	job := &schedulerv1pb.Job{
		DueTime: ptr.Of("1000s"),
		Data:    data,
	}
	meta := &schedulerv1pb.JobMetadata{
		Namespace: "default", AppId: "test",
		Target: &schedulerv1pb.JobTargetMetadata{
			Type: &schedulerv1pb.JobTargetMetadata_Job{
				Job: new(schedulerv1pb.TargetJob),
			},
		},
	}

	errCh := make(chan error)
	for i := range 10 {
		go func(i int) {
			for j := range 10 {
				_, serr := client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
					Name: "test-" + strconv.Itoa(i*10+j),
					Job:  job, Metadata: meta,
				})
				errCh <- serr
			}
		}(i)
	}

	for range 100 {
		require.NoError(t, <-errCh)
	}

	resp, err := client.ListJobs(ctx, &schedulerv1pb.ListJobsRequest{
		Metadata: &schedulerv1pb.JobMetadata{
			Namespace: "default", AppId: "test",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{
					Job: new(schedulerv1pb.TargetJob),
				},
			},
		},
	})
	require.NoError(t, err)
	assert.Len(t, resp.GetJobs(), 100)
}
