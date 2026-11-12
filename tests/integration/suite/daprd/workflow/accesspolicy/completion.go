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

package accesspolicy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(completion))
}

// completion gives the parent's app a deny-all WorkflowAccessPolicy and calls
// child workflows and activities on another app. Their results are replies to
// tasks the parent dispatched, not operations the other app performs on it,
// so the policy must let them through: every parent gets the real outcome,
// a child that finishes after its parent was terminated, purged or continued
// as new stops re-sending, and no ACL denial is recorded for the target app.
// A result for a pending task sent by any app other than the one the task
// was dispatched to never reaches the workflow.
//
// It runs with history signing on. completionnosigning and
// completionfastpath run the same test without signing and on the
// WorkflowsFastPath entry, which admits completions through a separate path.
type completion struct {
	workflow *workflow.Workflow
	parentID string
}

func (c *completion) Setup(t *testing.T) []framework.Option {
	return c.setup(t)
}

func (c *completion) setup(t *testing.T, opts ...workflow.Option) []framework.Option {
	c.parentID = "parent-" + uuid.NewString()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "policy.yaml"), fmt.Appendf(nil, `
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: deny-all
scopes:
- %s
spec: {}
`, c.parentID), 0o600))

	c.workflow = workflow.New(t, append([]workflow.Option{
		workflow.WithDaprds(2),
		workflow.WithMTLS(t),
		workflow.WithDaprdOptions(0, daprd.WithAppID(c.parentID), daprd.WithResourcesDir(dir)),
		workflow.WithDaprdOptions(1, daprd.WithAppID("target-"+uuid.NewString())),
	}, opts...)...)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *completion) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	targetID := c.workflow.DaprN(1).AppID()
	parent := c.workflow.RegistryN(0)
	target := c.workflow.RegistryN(1)

	release := make(chan struct{})
	localStarted := make(chan struct{}, 1)
	remoteStarted := make(chan struct{}, 1)
	blocking := func(started chan struct{}, out string) func(task.ActivityContext) (any, error) {
		return func(actx task.ActivityContext) (any, error) {
			select {
			case started <- struct{}{}:
			default:
			}
			select {
			case <-release:
				return out, nil
			case <-actx.Context().Done():
				return nil, actx.Context().Err()
			}
		}
	}

	require.NoError(t, target.AddWorkflowN("child-ok", func(*task.WorkflowContext) (any, error) {
		return "child-result", nil
	}))
	require.NoError(t, target.AddWorkflowN("child-fail", func(*task.WorkflowContext) (any, error) {
		return nil, errors.New("child-boom")
	}))
	require.NoError(t, target.AddWorkflowN("child-wait", func(ctx *task.WorkflowContext) (any, error) {
		return "child", ctx.WaitForSingleEvent("go", time.Minute).Await(nil)
	}))
	require.NoError(t, target.AddActivityN("act-ok", func(task.ActivityContext) (any, error) {
		return "activity-result", nil
	}))
	require.NoError(t, target.AddActivityN("act-fail", func(task.ActivityContext) (any, error) {
		return nil, errors.New("activity-boom")
	}))
	require.NoError(t, target.AddActivityN("remote-wait", blocking(remoteStarted, "remote")))
	require.NoError(t, parent.AddActivityN("local-wait", blocking(localStarted, "local")))
	require.NoError(t, parent.AddActivityN("local-echo", func(task.ActivityContext) (any, error) {
		return "echo", nil
	}))

	for _, name := range []string{"child-ok", "child-fail"} {
		require.NoError(t, parent.AddWorkflowN("parent-"+name, func(ctx *task.WorkflowContext) (any, error) {
			var out string
			err := ctx.CallChildWorkflow(name, task.WithChildWorkflowAppID(targetID)).Await(&out)
			return out, err
		}))
	}
	for _, name := range []string{"act-ok", "act-fail"} {
		require.NoError(t, parent.AddWorkflowN("parent-"+name, func(ctx *task.WorkflowContext) (any, error) {
			var out string
			err := ctx.CallActivity(name, task.WithActivityAppID(targetID)).Await(&out)
			return out, err
		}))
	}
	require.NoError(t, parent.AddWorkflowN("parent-orphan", func(ctx *task.WorkflowContext) (any, error) {
		var childID string
		if err := ctx.GetInput(&childID); err != nil {
			return nil, err
		}
		return nil, ctx.CallChildWorkflow("child-wait", task.WithChildWorkflowAppID(targetID), task.WithChildWorkflowInstanceID(childID)).Await(nil)
	}))
	const canChildID = "completion-can-child"
	require.NoError(t, parent.AddWorkflowN("parent-can", func(ctx *task.WorkflowContext) (any, error) {
		var gen int
		if err := ctx.GetInput(&gen); err != nil {
			return nil, err
		}
		if gen == 0 {
			// Left running: its result arrives after the parent continued as new.
			ctx.CallChildWorkflow("child-wait", task.WithChildWorkflowAppID(targetID), task.WithChildWorkflowInstanceID(canChildID))
			if err := ctx.WaitForSingleEvent("can", -1).Await(nil); err != nil {
				return nil, err
			}
			ctx.ContinueAsNew(1)
			return nil, nil
		}
		// Task 0 of this execution runs on the parent's own app.
		var out string
		if err := ctx.CallActivity("local-echo").Await(&out); err != nil {
			return nil, err
		}
		return out, ctx.WaitForSingleEvent("done", -1).Await(nil)
	}))
	const forgeChildID = "completion-forge-child"
	require.NoError(t, parent.AddWorkflowN("parent-forge", func(ctx *task.WorkflowContext) (any, error) {
		local := ctx.CallActivity("local-wait")
		remote := ctx.CallActivity("remote-wait", task.WithActivityAppID(targetID))
		child := ctx.CallChildWorkflow("child-wait", task.WithChildWorkflowAppID(targetID), task.WithChildWorkflowInstanceID(forgeChildID))
		var l, r, ch string
		if err := local.Await(&l); err != nil {
			return nil, err
		}
		if err := remote.Await(&r); err != nil {
			return nil, err
		}
		if err := child.Await(&ch); err != nil {
			return nil, err
		}
		return l + "," + r + "," + ch, nil
	}))

	client := c.workflow.BackendClientN(t, ctx, 0)
	targetClient := c.workflow.BackendClientN(t, ctx, 1)
	sched := c.workflow.Scheduler()

	for _, tc := range []struct {
		name    string
		output  string
		failure string
	}{
		{name: "child-ok", output: `"child-result"`},
		{name: "child-fail", failure: "child-boom"},
		{name: "act-ok", output: `"activity-result"`},
		{name: "act-fail", failure: "activity-boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, err := client.ScheduleNewWorkflow(ctx, "parent-"+tc.name)
			require.NoError(t, err)

			wctx, cancel := context.WithTimeout(ctx, time.Second*8)
			defer cancel()
			meta, err := client.WaitForWorkflowCompletion(wctx, id, api.WithFetchPayloads(true))
			require.NoError(t, err, "the result never reached the parent")

			if tc.failure == "" {
				assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
				assert.Equal(t, tc.output, meta.GetOutput().GetValue())
				return
			}
			assert.Equal(t, api.RUNTIME_STATUS_FAILED, meta.GetRuntimeStatus())
			assert.Contains(t, meta.GetFailureDetails().GetErrorMessage(), tc.failure)
		})
	}

	// A child that outlives its parent must be told the parent is done or
	// gone, not refused, otherwise it re-sends forever.
	for name, remove := range map[string]func(t *testing.T, id api.InstanceID){
		"purged": func(t *testing.T, id api.InstanceID) {
			require.NoError(t, client.PurgeWorkflowState(ctx, id, api.WithForcePurge(true), api.WithRecursivePurge(false)))
		},
		"terminated": func(t *testing.T, id api.InstanceID) {
			require.NoError(t, client.TerminateWorkflow(ctx, id, api.WithRecursiveTerminate(false)))
			wf.WaitForRuntimeStatus(t, ctx, client, id, api.RUNTIME_STATUS_TERMINATED)
		},
	} {
		t.Run("child finishes after its parent was "+name, func(t *testing.T) {
			childID := api.InstanceID("completion-orphan-" + name)
			id, err := client.ScheduleNewWorkflow(ctx, "parent-orphan", api.WithInput(childID))
			require.NoError(t, err)
			wf.WaitForRuntimeStatus(t, ctx, targetClient, childID, api.RUNTIME_STATUS_RUNNING)

			remove(t, id)

			require.NoError(t, targetClient.RaiseEvent(ctx, childID, "go"))
			wf.WaitForRuntimeStatus(t, ctx, targetClient, childID, api.RUNTIME_STATUS_COMPLETED)

			// Give a retry loop time to show itself before asserting there is none.
			time.Sleep(time.Second * 2)
			assert.Zero(t, sched.JobKeyCount(t, ctx, "parent-notify"), "the child is still re-sending its result")
		})
	}

	t.Run("child result from before the parent continued as new", func(t *testing.T) {
		id, err := client.ScheduleNewWorkflow(ctx, "parent-can", api.WithInput(0))
		require.NoError(t, err)
		wf.WaitForRuntimeStatus(t, ctx, targetClient, canChildID, api.RUNTIME_STATUS_RUNNING)

		require.NoError(t, client.RaiseEvent(ctx, id, "can"))
		require.EventuallyWithT(t, func(col *assert.CollectT) {
			hist, herr := client.GetInstanceHistory(ctx, id)
			require.NoError(col, herr)
			var done bool
			for _, e := range hist.GetEvents() {
				done = done || e.GetTaskCompleted() != nil
			}
			assert.True(col, done, "the new execution has not run its own task 0")
		}, time.Second*10, time.Millisecond*10)

		require.NoError(t, targetClient.RaiseEvent(ctx, canChildID, "go"))
		wf.WaitForRuntimeStatus(t, ctx, targetClient, canChildID, api.RUNTIME_STATUS_COMPLETED)

		// Give a retry loop time to show itself before asserting there is none.
		time.Sleep(time.Second * 2)
		assert.Zero(t, sched.JobKeyCount(t, ctx, "parent-notify"), "the child is still re-sending its result")
		completed, failed := wf.ChildCompletions(t, ctx, client, id, 0)
		assert.Zero(t, completed+failed, "a result from the previous execution reached the new one")

		require.NoError(t, client.RaiseEvent(ctx, id, "done"))
		wctx, cancel := context.WithTimeout(ctx, time.Second*8)
		defer cancel()
		meta, err := client.WaitForWorkflowCompletion(wctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
		assert.Equal(t, `"echo"`, meta.GetOutput().GetValue())
	})

	denied := "dapr_runtime_workflow_acl_action_denied_total|"
	for k := range c.workflow.DaprN(0).Metrics(t, ctx).All() {
		if strings.HasPrefix(k, denied) {
			assert.NotContains(t, k, "src_app_id:"+targetID, "a result from the dispatched app was denied")
		}
	}

	t.Run("forged results for pending tasks", func(t *testing.T) {
		id, err := client.ScheduleNewWorkflow(ctx, "parent-forge")
		require.NoError(t, err)
		for _, started := range []chan struct{}{localStarted, remoteStarted} {
			select {
			case <-started:
			case <-ctx.Done():
				require.Fail(t, "activity did not start")
			}
		}
		wf.WaitForRuntimeStatus(t, ctx, targetClient, forgeChildID, api.RUNTIME_STATUS_RUNNING)

		// Read the task ids, and the app each task went to, from the
		// parent's own history.
		ids := make(map[string]int32)
		require.EventuallyWithT(t, func(col *assert.CollectT) {
			hist, herr := client.GetInstanceHistory(ctx, id)
			require.NoError(col, herr)
			for _, e := range hist.GetEvents() {
				if ts := e.GetTaskScheduled(); ts != nil {
					ids[ts.GetName()] = e.GetEventId()
					if ts.GetName() == "remote-wait" {
						assert.Equal(col, targetID, e.GetRouter().GetTargetAppID())
					}
				}
				if cc := e.GetChildWorkflowInstanceCreated(); cc != nil {
					ids[cc.GetName()] = e.GetEventId()
					assert.Equal(col, targetID, e.GetRouter().GetTargetAppID())
				}
			}
			assert.Len(col, ids, 3)
		}, time.Second*10, time.Millisecond*10)

		localTask, remoteTask, childTask := ids["local-wait"], ids["remote-wait"], ids["child-wait"]
		failure := &protos.TaskFailureDetails{ErrorType: "Forged", ErrorMessage: "forged"}
		parentd := c.workflow.DaprN(0)
		forger := parentd.InternalGRPCClient(t, ctx, c.workflow.Sentry(), "forger-"+uuid.NewString(), parentd.Namespace())
		asTarget := parentd.InternalGRPCClient(t, ctx, c.workflow.Sentry(), targetID, parentd.Namespace())

		for _, tc := range []struct {
			name string
			from internalv1pb.ServiceInvocationClient
			task int32
			ev   *protos.HistoryEvent
		}{
			{
				name: "child workflow result from another app", from: forger, task: childTask,
				ev: &protos.HistoryEvent{EventType: &protos.HistoryEvent_ChildWorkflowInstanceCompleted{ChildWorkflowInstanceCompleted: &protos.ChildWorkflowInstanceCompletedEvent{TaskScheduledId: childTask, Result: wrapperspb.String(`"forged"`)}}},
			},
			{
				name: "child workflow failure from another app", from: forger, task: childTask,
				ev: &protos.HistoryEvent{EventType: &protos.HistoryEvent_ChildWorkflowInstanceFailed{ChildWorkflowInstanceFailed: &protos.ChildWorkflowInstanceFailedEvent{TaskScheduledId: childTask, FailureDetails: failure}}},
			},
			{
				name: "activity result from another app", from: forger, task: remoteTask,
				ev: &protos.HistoryEvent{EventType: &protos.HistoryEvent_TaskCompleted{TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: remoteTask, Result: wrapperspb.String(`"forged"`)}}},
			},
			{
				name: "activity failure from another app", from: forger, task: remoteTask,
				ev: &protos.HistoryEvent{EventType: &protos.HistoryEvent_TaskFailed{TaskFailed: &protos.TaskFailedEvent{TaskScheduledId: remoteTask, FailureDetails: failure}}},
			},
			{
				name: "dispatched app answering a task run by the parent's app", from: asTarget, task: localTask,
				ev: &protos.HistoryEvent{EventType: &protos.HistoryEvent_TaskCompleted{TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: localTask, Result: wrapperspb.String(`"forged"`)}}},
			},
			{
				name: "dispatched app answering a task that was never scheduled", from: asTarget, task: 99,
				ev: &protos.HistoryEvent{EventType: &protos.HistoryEvent_TaskCompleted{TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: 99, Result: wrapperspb.String(`"forged"`)}}},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				tc.ev.EventId, tc.ev.Timestamp = -1, timestamppb.Now()
				data, merr := proto.Marshal(tc.ev)
				require.NoError(t, merr)
				_, cerr := tc.from.CallActor(ctx, internalv1pb.NewInternalInvokeRequest("AddWorkflowEvent").
					WithActor("dapr.internal."+parentd.Namespace()+"."+c.parentID+".workflow", string(id)).
					WithData(data),
				)
				require.Error(t, cerr)
				assert.Contains(t, cerr.Error(), "access denied by workflow access policy")
				assert.Contains(t, cerr.Error(), fmt.Sprintf("was not dispatched task %d", tc.task))
			})
		}

		// A result claiming an earlier execution is acknowledged and dropped,
		// as a real child's late result would be, and never reaches the workflow.
		t.Run("child workflow result from another app claiming an earlier execution", func(t *testing.T) {
			data, merr := proto.Marshal(&protos.HistoryEvent{EventId: -1, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_ChildWorkflowInstanceCompleted{
				ChildWorkflowInstanceCompleted: &protos.ChildWorkflowInstanceCompletedEvent{TaskScheduledId: childTask, Result: wrapperspb.String(`"forged"`)},
			}})
			require.NoError(t, merr)
			_, cerr := forger.CallActor(ctx, internalv1pb.NewInternalInvokeRequest("AddWorkflowEvent").
				WithActor("dapr.internal."+parentd.Namespace()+"."+c.parentID+".workflow", string(id)).
				WithData(data).
				WithMetadata(map[string][]string{todo.MetadataParentExecutionID: {"earlier-execution"}}),
			)
			require.NoError(t, cerr)
		})

		close(release)
		require.NoError(t, targetClient.RaiseEvent(ctx, forgeChildID, "go"))
		wctx, cancel := context.WithTimeout(ctx, time.Second*8)
		defer cancel()
		meta, err := client.WaitForWorkflowCompletion(wctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
		assert.Equal(t, `"local,remote,child"`, meta.GetOutput().GetValue(), "a forged result reached the workflow")
	})

	sched.WaitJobKeyCount(t, ctx, "parent-notify", func(n int) bool { return n == 0 })
}
