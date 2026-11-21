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

package activitydispatch

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

// call is an activity a workflow on app 0 calls on the app at index app, and
// whether its run-activity reminder must carry the pull flag.
type call struct {
	name string
	app  int
	pull bool
}

// assertReminderFlags runs one workflow per call, holds its activity, and
// asserts the pull flag of the activity's reminder in the scheduler.
func assertReminderFlags(t *testing.T, ctx context.Context, wf *workflow.Workflow, calls ...call) {
	t.Helper()

	release := make(chan struct{})
	apps := map[int]bool{0: true}
	for _, c := range calls {
		apps[c.app] = true
		require.NoError(t, wf.Registry().AddWorkflowN("wf-"+c.name, func(ctx *task.WorkflowContext) (any, error) {
			return nil, ctx.CallActivity(c.name, task.WithActivityAppID(wf.DaprN(c.app).AppID())).Await(nil)
		}))
		require.NoError(t, wf.RegistryN(c.app).AddActivityN(c.name, func(ctx task.ActivityContext) (any, error) {
			select {
			case <-release:
			case <-ctx.Context().Done():
			}
			return nil, nil
		}))
	}
	client := wf.BackendClient(t, ctx)
	for app := range apps {
		if app != 0 {
			wf.BackendClientN(t, ctx, app)
		}
	}

	ids := make([]api.InstanceID, len(calls))
	for i, c := range calls {
		id, err := client.ScheduleNewWorkflow(ctx, "wf-"+c.name)
		require.NoError(t, err)
		ids[i] = id

		// In signing mode the scheduler only accepts mTLS clients.
		appID := wf.DaprN(c.app).AppID()
		var schedClient schedulerv1.SchedulerClient
		if wf.Signing() {
			schedClient = wf.Scheduler().ClientMTLS(t, ctx, appID)
		} else {
			schedClient = wf.Scheduler().Client(t, ctx)
		}

		assert.EventuallyWithT(t, func(col *assert.CollectT) {
			jobs, err := schedClient.ListJobs(ctx, &schedulerv1.ListJobsRequest{
				Metadata: &schedulerv1.JobMetadata{
					Namespace: "default", AppId: appID,
					Target: &schedulerv1.JobTargetMetadata{
						Type: &schedulerv1.JobTargetMetadata_Actor{
							Actor: &schedulerv1.TargetActorReminder{Type: wf.ActivityActorType(c.app)},
						},
					},
				},
			})
			if !assert.NoError(col, err) {
				return
			}
			var found bool
			for _, job := range jobs.GetJobs() {
				if strings.HasPrefix(job.GetMetadata().GetTarget().GetActor().GetId(), string(id)+"::") {
					found = true
					assert.Equal(col, c.pull, job.GetMetadata().GetPull(), "activity %s", c.name)
				}
			}
			assert.True(col, found, "no reminder for activity %s", c.name)
		}, time.Second*20, time.Millisecond*10)
	}

	close(release)
	for _, id := range ids {
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta))
	}
}
