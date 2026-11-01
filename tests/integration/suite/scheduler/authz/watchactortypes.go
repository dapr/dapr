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
	"testing"
	"time"

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

const watchActorTypesNamespace = "default"

func init() {
	suite.Register(new(watchactortypes))
}

// watchactortypes asserts an app cannot open a WatchJobs stream which
// declares, or sets a concurrency limit on, another app's reserved internal
// actor type (dapr.internal.<namespace>.<appid>.*), so it can neither receive
// nor throttle that app's reminder triggers, while its own internal types and
// user actor types remain allowed.
type watchactortypes struct {
	sentry    *sentry.Sentry
	scheduler *scheduler.Scheduler
}

func (w *watchactortypes) Setup(t *testing.T) []framework.Option {
	w.sentry = sentry.New(t)
	w.scheduler = scheduler.New(t,
		scheduler.WithSentry(w.sentry),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	return []framework.Option{
		framework.WithProcesses(w.sentry, w.scheduler),
	}
}

func (w *watchactortypes) Run(t *testing.T, ctx context.Context) {
	w.sentry.WaitUntilRunning(t, ctx)
	w.scheduler.WaitUntilRunning(t, ctx)

	appA := w.scheduler.ClientMTLS(t, ctx, "app-a")
	appB := w.scheduler.ClientMTLS(t, ctx, "app-b")

	const ownWF = "dapr.internal.default.app-a.workflow"
	const victimWF = "dapr.internal.default.app-b.workflow"
	const victimAct = "dapr.internal.default.app-b.activity"

	actorLimit := func(actorType string) *schedulerv1pb.ConcurrencyLimit {
		return &schedulerv1pb.ConcurrencyLimit{
			MaxConcurrent: 1,
			Target: &schedulerv1pb.ConcurrencyLimit_Actor{
				Actor: &schedulerv1pb.ConcurrencyLimitActor{Type: actorType},
			},
		}
	}

	watch := func(t *testing.T, initial *schedulerv1pb.WatchJobsRequestInitial) schedulerv1pb.Scheduler_WatchJobsClient {
		t.Helper()
		sctx, cancel := context.WithTimeout(ctx, time.Second*10)
		t.Cleanup(cancel)
		stream, err := appA.WatchJobs(sctx)
		require.NoError(t, err)
		initial.AppId = "app-a"
		initial.Namespace = watchActorTypesNamespace
		initial.AcceptJobTypes = []schedulerv1pb.JobTargetType{schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER}
		require.NoError(t, stream.Send(&schedulerv1pb.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{Initial: initial},
		}))
		return stream
	}

	denied := func(t *testing.T, stream schedulerv1pb.Scheduler_WatchJobsClient) {
		t.Helper()
		resp, err := stream.Recv()
		require.Error(t, err, "stream must be rejected, but app-a received trigger %q", resp.GetName())
		s, ok := status.FromError(err)
		require.True(t, ok, "%v", err)
		assert.Equal(t, codes.PermissionDenied, s.Code(), s.Message())
		assert.Contains(t, s.Message(), "is not allowed for app ID app-a in namespace default")
	}

	t.Run("declaring another app's internal actor type is denied", func(t *testing.T) {
		stream := watch(t, &schedulerv1pb.WatchJobsRequestInitial{ActorTypes: []string{victimWF}})
		_, err := appB.ScheduleJob(ctx, w.scheduler.JobNowActor("new-event", watchActorTypesNamespace, "app-b", victimWF, "instance1"))
		require.NoError(t, err)
		denied(t, stream)
	})

	t.Run("concurrency limit on another app's internal actor type is denied", func(t *testing.T) {
		denied(t, watch(t, &schedulerv1pb.WatchJobsRequestInitial{
			ActorTypes:        []string{ownWF},
			ConcurrencyLimits: []*schedulerv1pb.ConcurrencyLimit{actorLimit(victimAct)},
		}))
	})

	t.Run("own internal and user actor types are allowed", func(t *testing.T) {
		stream := watch(t, &schedulerv1pb.WatchJobsRequestInitial{
			ActorTypes:        []string{ownWF, "myactor"},
			ConcurrencyLimits: []*schedulerv1pb.ConcurrencyLimit{actorLimit(ownWF), actorLimit("myactor")},
		})
		_, err := appA.ScheduleJob(ctx, w.scheduler.JobNowActor("own-event", watchActorTypesNamespace, "app-a", ownWF, "instance1"))
		require.NoError(t, err)

		resp, err := stream.Recv()
		require.NoError(t, err)
		assert.Equal(t, "own-event", resp.GetName())
		require.NoError(t, stream.Send(&schedulerv1pb.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
				Result: &schedulerv1pb.WatchJobsRequestResult{
					Id:     resp.GetId(),
					Status: schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS,
				},
			},
		}))
	})
}
