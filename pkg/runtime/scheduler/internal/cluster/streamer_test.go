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

package cluster

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	wfenginefake "github.com/dapr/dapr/pkg/runtime/wfengine/fake"
)

func Test_handleJob_ErrReminderCanceled_remoteGRPC(t *testing.T) {
	t.Parallel()

	const (
		workflowActorType = "dapr.internal.default.myapp.workflow"
		activityActorType = "dapr.internal.default.myapp.activity"
		userActorType     = "myactortype"
	)

	cases := map[string]struct {
		actorType string
		want      schedulerv1pb.WatchJobsRequestResultStatus
	}{
		"user actor with ErrReminderCanceled message: SUCCESS (delete)": {
			actorType: userActorType,
			want:      schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS,
		},
		"internal workflow actor with ErrReminderCanceled message: FAILED (retry)": {
			actorType: workflowActorType,
			want:      schedulerv1pb.WatchJobsRequestResultStatus_FAILED,
		},
		"internal activity actor with ErrReminderCanceled message: FAILED (retry)": {
			actorType: activityActorType,
			want:      schedulerv1pb.WatchJobsRequestResultStatus_FAILED,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			actors := routerfake.New().WithCallReminderFn(
				func(context.Context, *actorapi.Reminder) error {
					return status.Error(codes.Unknown, actorerrors.ErrReminderCanceled.Error())
				},
			)

			s := &streamer{
				actors:   actors,
				wfengine: wfenginefake.New(),
			}

			job := &schedulerv1pb.WatchJobsResponse{
				Name: "test-job",
				Metadata: &schedulerv1pb.JobMetadata{
					Target: &schedulerv1pb.JobTargetMetadata{
						Type: &schedulerv1pb.JobTargetMetadata_Actor{
							Actor: &schedulerv1pb.TargetActorReminder{
								Type: tc.actorType,
								Id:   "instance-1",
							},
						},
					},
				},
			}

			got := s.handleJob(t.Context(), job)
			assert.Equal(t, tc.want, got)
		})
	}
}

func Test_handleJob_ErrReminderCanceled_localSentinel(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		actorType string
	}{
		"user actor":              {actorType: "myactortype"},
		"internal workflow actor": {actorType: "dapr.internal.default.myapp.workflow"},
		"internal activity actor": {actorType: "dapr.internal.default.myapp.activity"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			actors := routerfake.New().WithCallReminderFn(
				func(context.Context, *actorapi.Reminder) error {
					return actorerrors.ErrReminderCanceled
				},
			)

			s := &streamer{
				actors:   actors,
				wfengine: wfenginefake.New(),
			}

			job := &schedulerv1pb.WatchJobsResponse{
				Name: "test-job",
				Metadata: &schedulerv1pb.JobMetadata{
					Target: &schedulerv1pb.JobTargetMetadata{
						Type: &schedulerv1pb.JobTargetMetadata_Actor{
							Actor: &schedulerv1pb.TargetActorReminder{
								Type: tc.actorType,
								Id:   "instance-1",
							},
						},
					},
				},
			}

			got := s.handleJob(t.Context(), job)
			assert.Equal(t, schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS, got)
		})
	}
}
