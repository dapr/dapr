/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package authz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(mtls))
}

// mtls tests scheduler with tls enabled.
type mtls struct {
	sentry    *sentry.Sentry
	scheduler *scheduler.Scheduler
}

func (m *mtls) Setup(t *testing.T) []framework.Option {
	m.sentry = sentry.New(t)
	m.scheduler = scheduler.New(t, scheduler.WithSentry(m.sentry))

	return []framework.Option{
		framework.WithProcesses(m.sentry, m.scheduler),
	}
}

func (m *mtls) Run(t *testing.T, ctx context.Context) {
	m.sentry.WaitUntilRunning(t, ctx)
	m.scheduler.WaitUntilRunning(t, ctx)

	client := m.scheduler.ClientMTLS(t, ctx, "foo")

	req := &schedulerv1pb.ScheduleJobRequest{
		Name: "testJob",
		Job: &schedulerv1pb.Job{
			Schedule: ptr.Of("@daily"),
		},
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     "foo",
			Namespace: "default",
			Type: &schedulerv1pb.JobMetadataType{
				Type: &schedulerv1pb.JobMetadataType_Job{
					Job: new(schedulerv1pb.TypeJob),
				},
			},
		},
	}

	_, err := client.ScheduleJob(ctx, req)
	require.NoError(t, err)

	type tcase struct {
		funcGoodAppID func() error
		funcBadAppID  func() error
	}
	tests := map[string]tcase{
		"ScheduleJob": {
			funcGoodAppID: func() error {
				_, err := client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
					Name: "testJob",
					Job:  &schedulerv1pb.Job{Schedule: ptr.Of("@daily")},
					Metadata: &schedulerv1pb.JobMetadata{
						AppId:     "foo",
						Namespace: "default",
						Type: &schedulerv1pb.JobMetadataType{
							Type: &schedulerv1pb.JobMetadataType_Job{Job: new(schedulerv1pb.TypeJob)},
						},
					},
				})
				return err
			},
			funcBadAppID: func() error {
				_, err := client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
					Name: "testJob",
					Job:  &schedulerv1pb.Job{Schedule: ptr.Of("@daily")},
					Metadata: &schedulerv1pb.JobMetadata{
						AppId:     "not-foo",
						Namespace: "default",
						Type: &schedulerv1pb.JobMetadataType{
							Type: &schedulerv1pb.JobMetadataType_Job{Job: new(schedulerv1pb.TypeJob)},
						},
					},
				})
				return err
			},
		},
		"GetJob": {
			funcGoodAppID: func() error {
				_, err := client.GetJob(ctx, &schedulerv1pb.GetJobRequest{
					Name: "testJob",
					Metadata: &schedulerv1pb.JobMetadata{
						AppId:     "foo",
						Namespace: "default",
						Type: &schedulerv1pb.JobMetadataType{
							Type: &schedulerv1pb.JobMetadataType_Job{Job: new(schedulerv1pb.TypeJob)},
						},
					},
				})
				return err
			},
			funcBadAppID: func() error {
				_, err := client.GetJob(ctx, &schedulerv1pb.GetJobRequest{
					Name: "testJob",
					Metadata: &schedulerv1pb.JobMetadata{
						AppId:     "not-foo",
						Namespace: "default",
						Type: &schedulerv1pb.JobMetadataType{
							Type: &schedulerv1pb.JobMetadataType_Job{Job: new(schedulerv1pb.TypeJob)},
						},
					},
				})
				return err
			},
		},
		"DeleteJob": {
			funcGoodAppID: func() error {
				_, err := client.DeleteJob(ctx, &schedulerv1pb.DeleteJobRequest{
					Name: "testJob",
					Metadata: &schedulerv1pb.JobMetadata{
						AppId:     "foo",
						Namespace: "default",
						Type: &schedulerv1pb.JobMetadataType{
							Type: &schedulerv1pb.JobMetadataType_Job{Job: new(schedulerv1pb.TypeJob)},
						},
					},
				})
				return err
			},
			funcBadAppID: func() error {
				_, err := client.DeleteJob(ctx, &schedulerv1pb.DeleteJobRequest{
					Name: "testJob",
					Metadata: &schedulerv1pb.JobMetadata{
						AppId:     "not-foo",
						Namespace: "default",
						Type: &schedulerv1pb.JobMetadataType{
							Type: &schedulerv1pb.JobMetadataType_Job{Job: new(schedulerv1pb.TypeJob)},
						},
					},
				})
				return err
			},
		},
		"WatchJobs": {
			funcGoodAppID: func() error {
				return nil
			},
			funcBadAppID: func() error {
				stream, err := client.WatchJobs(ctx)
				require.NoError(t, err)
				require.NoError(t, stream.Send(&schedulerv1pb.WatchJobsRequest{
					WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{
						Initial: &schedulerv1pb.WatchJobsRequestInitial{
							AppId:     "not-foo",
							Namespace: "default",
						},
					},
				}))
				_, err = stream.Recv()
				return err
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			err := test.funcBadAppID()
			s, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.PermissionDenied, s.Code())
			assert.Contains(t, s.Message(), "identity does not match request")

			err = test.funcGoodAppID()
			require.NoError(t, err)
		})
	}
}
