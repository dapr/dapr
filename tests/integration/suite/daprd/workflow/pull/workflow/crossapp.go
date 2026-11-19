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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(crossapp))
}

// crossapp checks each app's Configuration decides whether its own workflow
// workflow turns are pull: a child on a pulling app pulls, its hashed caller does not.
type crossapp struct {
	workflow *workflow.Workflow
}

func (c *crossapp) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithAppID("workflow-crossapp-caller")),
		workflow.WithDaprdOptions(1, daprd.WithAppID("workflow-crossapp-callee"), daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowcrossappcallee
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 4
    maxConcurrentActivityInvocations: 4
    dispatchMode: pull
    priority:
      default:
        priorityLevel: low
        fairnessWeight: 2
      workflows:
        - name: child
          priorityLevel: critical
          fairnessWeight: 6
`)),
	)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *crossapp) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const (
		parentID = "crossapp-parent"
		childID  = "crossapp-child"
	)
	callerAppID := c.workflow.DaprN(0).AppID()
	calleeAppID := c.workflow.DaprN(1).AppID()

	require.NoError(t, c.workflow.Registry().AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		child := ctx.CallChildWorkflow("child",
			task.WithChildWorkflowAppID(calleeAppID),
			task.WithChildWorkflowInstanceID(childID))
		if err := ctx.CreateTimer(time.Hour).Await(nil); err != nil {
			return nil, err
		}
		return nil, child.Await(nil)
	}))
	require.NoError(t, c.workflow.RegistryN(1).AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CreateTimer(time.Hour).Await(nil)
	}))

	client0 := c.workflow.BackendClient(t, ctx)
	client1 := c.workflow.BackendClientN(t, ctx, 1)

	schedClients := make([]schedulerv1.SchedulerClient, 2)
	for i, appID := range []string{callerAppID, calleeAppID} {
		if c.workflow.Signing() {
			schedClients[i] = c.workflow.Scheduler().ClientMTLS(t, ctx, appID)
		} else {
			schedClients[i] = c.workflow.Scheduler().Client(t, ctx)
		}
	}
	// timer returns the metadata of the timer reminder of instance id on the
	// workflow actor type of app index.
	timer := func(col *assert.CollectT, index int, appID, id string) *schedulerv1.JobMetadata {
		jobs, err := schedClients[index].ListJobs(ctx, &schedulerv1.ListJobsRequest{
			Metadata: &schedulerv1.JobMetadata{
				Namespace: "default", AppId: appID,
				Target: &schedulerv1.JobTargetMetadata{
					Type: &schedulerv1.JobTargetMetadata_Actor{
						Actor: &schedulerv1.TargetActorReminder{Type: c.workflow.WorkflowActorType(index)},
					},
				},
			},
		})
		if !assert.NoError(col, err) {
			return nil
		}
		for _, job := range jobs.GetJobs() {
			if job.GetMetadata().GetTarget().GetActor().GetId() == id && strings.Contains(job.GetName(), "timer-") {
				return job.GetMetadata()
			}
		}
		assert.Fail(col, "no timer reminder", id)
		return nil
	}

	_, err := client0.ScheduleNewWorkflow(ctx, "parent",
		api.WithInstanceID(parentID))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		// The child runs on the callee, which pulls, so its timer is pull and
		// carries the child config entry, critical 6.
		if meta := timer(col, 1, calleeAppID, childID); meta != nil {
			assert.True(col, meta.GetPull())
			assert.Equal(col, schedulerv1.PriorityLevel_PRIORITY_LEVEL_CRITICAL, meta.GetPriority().GetLevel())
			assert.Equal(col, uint32(6), meta.GetPriority().GetFairnessWeight())
		}
		// The caller does not pull, so its own timer is not pull and carries no priority.
		if meta := timer(col, 0, callerAppID, parentID); meta != nil {
			assert.False(col, meta.GetPull())
			assert.Nil(col, meta.GetPriority())
		}
	}, time.Second*10, time.Millisecond*10)

	// Terminating the parent terminates the child on the callee too.
	require.NoError(t, client0.TerminateWorkflow(ctx, parentID,
		api.WithRecursiveTerminate(true)))
	_, err = client0.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	_, err = client1.WaitForWorkflowCompletion(ctx, childID)
	require.NoError(t, err)
}
