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

package api

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"

	corev1 "github.com/dapr/dapr/pkg/proto/common/v1"
	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(failurepolicy))
}

type failurepolicy struct {
	scheduler *scheduler.Scheduler
}

func (f *failurepolicy) Setup(t *testing.T) []framework.Option {
	f.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(f.scheduler),
	}
}

func (f *failurepolicy) Run(t *testing.T, ctx context.Context) {
	f.scheduler.WaitUntilRunning(t, ctx)

	client := f.scheduler.Client(t, ctx)

	metadata := &schedulerv1.JobMetadata{
		Namespace: "namespace", AppId: "appid",
		Target: &schedulerv1.JobTargetMetadata{
			Type: new(schedulerv1.JobTargetMetadata_Job),
		},
	}

	_, err := client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name: "test1", Metadata: metadata,
		Job: &schedulerv1.Job{
			DueTime:       ptr.Of("100s"),
			FailurePolicy: nil,
		},
	})
	require.NoError(t, err)
	resp, err := client.GetJob(ctx, &schedulerv1.GetJobRequest{
		Name: "test1", Metadata: metadata,
	})
	require.NoError(t, err)
	assert.Equal(t, &corev1.JobFailurePolicy{
		Policy: &corev1.JobFailurePolicy_Constant{
			Constant: &corev1.JobFailurePolicyConstant{
				Interval:   durationpb.New(time.Second * 1),
				MaxRetries: ptr.Of(uint32(3)),
			},
		},
	}, resp.GetJob().GetFailurePolicy())

	_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name: "test2", Metadata: metadata,
		Job: &schedulerv1.Job{
			DueTime: ptr.Of("100s"),
			FailurePolicy: &corev1.JobFailurePolicy{
				Policy: &corev1.JobFailurePolicy_Constant{
					Constant: &corev1.JobFailurePolicyConstant{
						Interval:   durationpb.New(time.Second * 10),
						MaxRetries: ptr.Of(uint32(1234)),
					},
				},
			},
		},
	})
	require.NoError(t, err)
	resp, err = client.GetJob(ctx, &schedulerv1.GetJobRequest{
		Name: "test2", Metadata: metadata,
	})
	require.NoError(t, err)
	assert.Equal(t, &corev1.JobFailurePolicy{
		Policy: &corev1.JobFailurePolicy_Constant{
			Constant: &corev1.JobFailurePolicyConstant{
				Interval:   durationpb.New(time.Second * 10),
				MaxRetries: ptr.Of(uint32(1234)),
			},
		},
	}, resp.GetJob().GetFailurePolicy())

	_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name: "test3", Metadata: metadata,
		Job: &schedulerv1.Job{
			DueTime: ptr.Of("100s"),
			FailurePolicy: &corev1.JobFailurePolicy{
				Policy: &corev1.JobFailurePolicy_Drop{
					Drop: new(corev1.JobFailurePolicyDrop),
				},
			},
		},
	})
	require.NoError(t, err)
	resp, err = client.GetJob(ctx, &schedulerv1.GetJobRequest{
		Name: "test3", Metadata: metadata,
	})
	require.NoError(t, err)
	assert.Equal(t, &corev1.JobFailurePolicy{
		Policy: &corev1.JobFailurePolicy_Drop{
			Drop: new(corev1.JobFailurePolicyDrop),
		},
	}, resp.GetJob().GetFailurePolicy())

	listResp, err := client.ListJobs(ctx, &schedulerv1.ListJobsRequest{Metadata: metadata})
	require.NoError(t, err)
	gotFPs := make([]*corev1.JobFailurePolicy, 0, 3)
	for _, j := range listResp.GetJobs() {
		gotFPs = append(gotFPs, j.GetJob().GetFailurePolicy())
	}
	assert.ElementsMatch(t, []*corev1.JobFailurePolicy{
		{
			Policy: &corev1.JobFailurePolicy_Constant{
				Constant: &corev1.JobFailurePolicyConstant{
					Interval:   durationpb.New(time.Second * 10),
					MaxRetries: ptr.Of(uint32(1234)),
				},
			},
		},
		{
			Policy: &corev1.JobFailurePolicy_Constant{
				Constant: &corev1.JobFailurePolicyConstant{
					Interval:   durationpb.New(time.Second * 1),
					MaxRetries: ptr.Of(uint32(3)),
				},
			},
		},
		{
			Policy: &corev1.JobFailurePolicy_Drop{
				Drop: new(corev1.JobFailurePolicyDrop),
			},
		},
	}, gotFPs)
}
