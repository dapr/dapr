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

package authz

import (
	"context"
	"strings"
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
)

func init() {
	suite.Register(new(internalactor))
}

// internalactor asserts an app cannot schedule, read or delete actor
// reminder jobs on another app's reserved internal actor types
// (dapr.internal.<namespace>.<appid>.*), also not through a type prefix
// delete, while its own internal types, user actor types hosted by other
// apps, and creating (never reading, replacing or deleting) cross-app
// activity result reminders remain allowed.
type internalactor struct {
	sentry    *sentry.Sentry
	scheduler *scheduler.Scheduler
}

func (i *internalactor) Setup(t *testing.T) []framework.Option {
	i.sentry = sentry.New(t)
	i.scheduler = scheduler.New(t,
		scheduler.WithSentry(i.sentry),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	return []framework.Option{
		framework.WithProcesses(i.sentry, i.scheduler),
	}
}

func (i *internalactor) Run(t *testing.T, ctx context.Context) {
	i.sentry.WaitUntilRunning(t, ctx)
	i.scheduler.WaitUntilRunning(t, ctx)

	client := i.scheduler.ClientMTLS(t, ctx, "other")

	meta := func(actorType string) *schedulerv1pb.JobMetadata {
		return &schedulerv1pb.JobMetadata{
			AppId:     "other",
			Namespace: "default",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{Id: "instance1", Type: actorType},
				},
			},
		}
	}
	schedule := func(name, actorType string) error {
		_, err := client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
			Name:     name,
			Job:      &schedulerv1pb.Job{DueTime: new("1000s")},
			Metadata: meta(actorType),
		})
		return err
	}
	denied := func(t *testing.T, err error) {
		t.Helper()
		s, ok := status.FromError(err)
		require.True(t, ok, "%v", err)
		assert.Equal(t, codes.PermissionDenied, s.Code())
		assert.Contains(t, s.Message(), "is not allowed for app ID other in namespace default")
	}

	const targetWF = "dapr.internal.default.target.workflow"
	const targetAct = "dapr.internal.default.target.activity"

	t.Run("schedule on another app's internal types is denied", func(t *testing.T) {
		denied(t, schedule("new-event", targetWF))
		denied(t, schedule("timer-1", targetWF))
		denied(t, schedule("run-activity", targetAct))
		denied(t, schedule("new-event", "dapr.internal.other.other.workflow"))
		denied(t, schedule("activity-result-abc", targetAct))
	})

	t.Run("schedule on own internal types and user types is allowed", func(t *testing.T) {
		require.NoError(t, schedule("new-event", "dapr.internal.default.other.workflow"))
		require.NoError(t, schedule("run-activity", "dapr.internal.default.other.activity"))
		require.NoError(t, schedule("remind", "target-user-actor-type"))
	})

	t.Run("activity result on another app's workflow is allowed to create only", func(t *testing.T) {
		require.NoError(t, schedule("activity-result-abc.exec1", targetWF))

		_, err := client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
			Name:      "activity-result-abc.exec1",
			Overwrite: true,
			Job:       &schedulerv1pb.Job{DueTime: new("1000s")},
			Metadata:  meta(targetWF),
		})
		denied(t, err)
		_, err = client.GetJob(ctx, &schedulerv1pb.GetJobRequest{Name: "activity-result-abc.exec1", Metadata: meta(targetWF)})
		denied(t, err)
		_, err = client.DeleteJob(ctx, &schedulerv1pb.DeleteJobRequest{Name: "activity-result-abc.exec1", Metadata: meta(targetWF)})
		denied(t, err)
	})

	t.Run("a type prefix delete cannot reach another app's internal reminders", func(t *testing.T) {
		target := i.scheduler.ClientMTLS(t, ctx, "target")
		targetMeta := &schedulerv1pb.JobMetadata{
			AppId:     "target",
			Namespace: "default",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{Id: "instance1", Type: targetWF},
				},
			},
		}
		_, err := target.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
			Name:     "new-event",
			Job:      &schedulerv1pb.Job{DueTime: new("1000s")},
			Metadata: targetMeta,
		})
		require.NoError(t, err)

		// "dapr" is not a reserved internal type, so the request is
		// authorized, but as a key prefix with an empty actor ID it must
		// not cover "dapr.internal.default.target.workflow".
		prefixMeta := func(actorType string) *schedulerv1pb.JobMetadata {
			m := meta(actorType)
			m.GetTarget().GetActor().Id = ""
			return m
		}
		_, err = client.DeleteByMetadata(ctx, &schedulerv1pb.DeleteByMetadataRequest{
			IdPrefixMatch: new(true),
			Metadata:      prefixMeta("dapr"),
		})
		require.NoError(t, err)
		_, err = client.DeleteByMetadata(ctx, &schedulerv1pb.DeleteByMetadataRequest{
			IdPrefixMatch: new(true),
			Metadata:      prefixMeta("dapr.internal.default.target"),
		})
		denied(t, err)

		_, err = target.GetJob(ctx, &schedulerv1pb.GetJobRequest{Name: "new-event", Metadata: targetMeta})
		require.NoError(t, err, "the target's reminder must survive another app's prefix delete")
		assert.Contains(t, strings.Join(i.scheduler.ListAllKeys(t, ctx, "dapr/jobs"), "\n"),
			"actorreminder||default||dapr.internal.default.target.workflow||instance1||new-event")
	})

	t.Run("read and delete on another app's internal type is denied", func(t *testing.T) {
		_, err := client.GetJob(ctx, &schedulerv1pb.GetJobRequest{Name: "new-event", Metadata: meta(targetWF)})
		denied(t, err)
		_, err = client.DeleteJob(ctx, &schedulerv1pb.DeleteJobRequest{Name: "new-event", Metadata: meta(targetWF)})
		denied(t, err)
		_, err = client.ListJobs(ctx, &schedulerv1pb.ListJobsRequest{Metadata: meta(targetWF)})
		denied(t, err)
		_, err = client.DeleteByMetadata(ctx, &schedulerv1pb.DeleteByMetadataRequest{Metadata: meta(targetWF)})
		denied(t, err)
		_, err = client.DeleteByNamePrefix(ctx, &schedulerv1pb.DeleteByNamePrefixRequest{NamePrefix: "activity-result-", Metadata: meta(targetWF)})
		denied(t, err)
	})
}
