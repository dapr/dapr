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

package workflow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	procworkflow "github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/durabletask-go/api/protos"
)

// PlantReminder schedules a due-now reminder named name against w's workflow
// actor for instanceID, carrying ev as its data and the retry-forever failure
// policy real activity-result reminders are created with. It delivers an event
// the runtime would not produce on its own, which is how a test reaches the
// reminder-driven admission path directly. mtls selects the app's own identity
// for the schedule call, which a deployment running with sentry demands.
func PlantReminder(t *testing.T, ctx context.Context, w *procworkflow.Workflow, mtls bool, instanceID, name string, ev *protos.HistoryEvent) {
	t.Helper()

	data, err := anypb.New(ev)
	require.NoError(t, err)

	sched, appID := w.Scheduler(), w.Dapr().AppID()
	req := sched.JobNowActor(name, "default", appID, "dapr.internal.default."+appID+".workflow", instanceID)
	req.Job.Data = data
	req.Job.FailurePolicy = common.RetryForeverPolicy()

	// Only the client actually needed is built: a scheduler running with
	// sentry refuses the insecure one, and dialling it blocks until the
	// deadline.
	client := schedulerv1pb.SchedulerClient(nil)
	if mtls {
		client = sched.ClientMTLS(t, ctx, appID)
	} else {
		client = sched.Client(t, ctx)
	}
	_, err = client.ScheduleJob(ctx, req)
	require.NoError(t, err)
}
