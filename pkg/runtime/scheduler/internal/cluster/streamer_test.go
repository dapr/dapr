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
	"sync/atomic"
	"testing"

	"github.com/cenkalti/backoff/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity"
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

func Test_handleJob_pullMetadataExecutesLocally(t *testing.T) {
	t.Parallel()

	const activityType = "dapr.internal.default.myapp.activity"
	for name, tc := range map[string]struct {
		pull          *bool
		pullDispatch  bool
		pullActorType string
		want          bool
	}{
		"pull job on a sidecar offering slots executes locally": {pull: new(true), pullDispatch: true, pullActorType: activityType, want: true},
		"pull job on a sidecar without slots forwards":          {pull: new(true), pullDispatch: false, want: false},
		"pull job of another actor type forwards":               {pull: new(true), pullDispatch: true, pullActorType: "other", want: false},
		"pull false forwards":                                   {pull: new(false), pullDispatch: true, pullActorType: activityType, want: false},
		"pull unset forwards":                                   {pull: nil, pullDispatch: true, pullActorType: activityType, want: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got *actorapi.Reminder
			actors := routerfake.New().WithCallReminderFn(
				func(_ context.Context, rem *actorapi.Reminder) error {
					got = rem
					return nil
				},
			)
			s := &streamer{actors: actors, wfengine: wfenginefake.New(), pullActorType: tc.pullActorType}
			if tc.pullDispatch {
				s.pull = NewPull(t.Context())
			}

			job := &schedulerv1pb.WatchJobsResponse{
				Name: "run-activity",
				Metadata: &schedulerv1pb.JobMetadata{
					Pull: tc.pull,
					Target: &schedulerv1pb.JobTargetMetadata{
						Type: &schedulerv1pb.JobTargetMetadata_Actor{
							Actor: &schedulerv1pb.TargetActorReminder{Type: activityType, Id: "wf::1::0"},
						},
					},
				},
			}

			assert.Equal(t, schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS, s.handleJob(t.Context(), job))
			require.NotNil(t, got)
			assert.Equal(t, tc.want, got.ExecuteLocally)
		})
	}
}

func Test_handleJob_noCapacity(t *testing.T) {
	t.Parallel()

	actors := routerfake.New().WithCallReminderFn(func(context.Context, *actorapi.Reminder) error {
		return backoff.Permanent(activity.ErrNoCapacity)
	})
	s := &streamer{actors: actors, wfengine: wfenginefake.New()}
	job := &schedulerv1pb.WatchJobsResponse{
		Name: "run-activity",
		Metadata: &schedulerv1pb.JobMetadata{
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{Type: "dapr.internal.default.myapp.activity", Id: "wf::1::0"},
				},
			},
		},
	}
	assert.Equal(t, schedulerv1pb.WatchJobsRequestResultStatus_NO_CAPACITY, s.handleJob(t.Context(), job),
		"a refused pull delivery is queued again by the scheduler, not retried as a failure")
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

func Test_handleJob_invalidTargetMetadata(t *testing.T) {
	t.Parallel()

	cases := map[string]*schedulerv1pb.WatchJobsResponse{
		"nil metadata": {
			Name: "test-job",
		},
		"nil target": {
			Name:     "test-job",
			Metadata: &schedulerv1pb.JobMetadata{},
		},
		"nil target type": {
			Name: "test-job",
			Metadata: &schedulerv1pb.JobMetadata{
				Target: &schedulerv1pb.JobTargetMetadata{},
			},
		},
		"typed-nil actor wrapper": {
			Name: "test-job",
			Metadata: &schedulerv1pb.JobMetadata{
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: (*schedulerv1pb.JobTargetMetadata_Actor)(nil),
				},
			},
		},
		"nil actor payload": {
			Name: "test-job",
			Metadata: &schedulerv1pb.JobMetadata{
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Actor{},
				},
			},
		},
		"empty actor type": {
			Name: "test-job",
			Metadata: &schedulerv1pb.JobMetadata{
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Actor{
						Actor: &schedulerv1pb.TargetActorReminder{Id: "instance-1"},
					},
				},
			},
		},
		"empty actor id": {
			Name: "test-job",
			Metadata: &schedulerv1pb.JobMetadata{
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Actor{
						Actor: &schedulerv1pb.TargetActorReminder{Type: "myactortype"},
					},
				},
			},
		},
	}

	for name, job := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var called atomic.Bool
			actors := routerfake.New().WithCallReminderFn(
				func(context.Context, *actorapi.Reminder) error {
					called.Store(true)
					return nil
				},
			)

			s := &streamer{
				actors:   actors,
				wfengine: wfenginefake.New(),
			}

			var got schedulerv1pb.WatchJobsRequestResultStatus
			require.NotPanics(t, func() {
				got = s.handleJob(t.Context(), job)
			})
			assert.Equal(t, schedulerv1pb.WatchJobsRequestResultStatus_FAILED, got)
			assert.False(t, called.Load(), "reminder must not be invoked for invalid target metadata")
		})
	}
}

func Test_invokeActorReminder_SourceAppID(t *testing.T) {
	t.Parallel()

	var got *actorapi.Reminder
	actors := routerfake.New().WithCallReminderFn(
		func(_ context.Context, r *actorapi.Reminder) error {
			got = r
			return nil
		},
	)
	s := &streamer{actors: actors, wfengine: wfenginefake.New()}

	actor := &schedulerv1pb.TargetActorReminder{Type: "dapr.internal.default.target.workflow", Id: "instance-1"}
	job := &schedulerv1pb.WatchJobsResponse{
		Name: "activity-result-abc",
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     "creator",
			Namespace: "default",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{Actor: actor},
			},
		},
	}

	require.NoError(t, s.invokeActorReminder(t.Context(), job, actor))
	require.NotNil(t, got)
	assert.Equal(t, "creator", got.SourceAppID)
	assert.Equal(t, "activity-result-abc", got.Name)
	assert.Equal(t, "dapr.internal.default.target.workflow", got.ActorType)
	assert.Equal(t, "instance-1", got.ActorID)
}
