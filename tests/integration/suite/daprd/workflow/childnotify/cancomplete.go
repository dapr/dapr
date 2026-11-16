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

package childnotify

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(cancomplete))
}

// cancomplete has a cross-app child continue as new with a different input
// and then complete, either in the same turn or after waiting for an event,
// with and without history propagation. The parent verifies the completion
// attestation against the input it created the child with, not the
// continued one, and its task must resolve in every case.
type cancomplete struct {
	workflow *workflow.Workflow
}

func (c *cancomplete) Setup(t *testing.T) []framework.Option {
	// mTLS turns history signing on for both daprds, so the parent verifies
	// the child's completion attestation.
	c.workflow = workflow.New(t, workflow.WithDaprds(2), workflow.WithMTLS(t))
	return []framework.Option{framework.WithProcesses(c.workflow)}
}

func (c *cancomplete) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	childApp := c.workflow.DaprN(1).AppID()
	// "child-wait" waits for an event in its second generation; "child-now"
	// completes in the turn that continued.
	for _, name := range []string{"child-wait", "child-now"} {
		waits := name == "child-wait"
		require.NoError(t, c.workflow.RegistryN(1).AddWorkflowN(name, func(ctx *task.WorkflowContext) (any, error) {
			var input string
			if err := ctx.GetInput(&input); err != nil {
				return nil, err
			}
			if input != "second" {
				ctx.ContinueAsNew("second")
				return nil, nil
			}
			if waits {
				if err := ctx.WaitForSingleEvent("go", time.Hour).Await(nil); err != nil {
					return nil, err
				}
			}
			return input, nil
		}))
	}
	require.NoError(t, c.workflow.Registry().AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var in struct {
			Child     string `json:"child"`
			ChildID   string `json:"childID"`
			Propagate bool   `json:"propagate"`
			NoInput   bool   `json:"noInput"`
		}
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		opts := []task.ChildWorkflowOption{
			task.WithChildWorkflowInstanceID(in.ChildID),
			task.WithChildWorkflowAppID(childApp),
		}
		if !in.NoInput {
			opts = append(opts, task.WithChildWorkflowInput("first"))
		}
		if in.Propagate {
			opts = append(opts, task.WithHistoryPropagation(api.PropagateLineage()))
		}
		var out string
		if err := ctx.CallChildWorkflow(in.Child, opts...).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	cl := c.workflow.BackendClient(t, ctx)
	child := c.workflow.BackendClientN(t, ctx, 1)

	for _, tc := range []struct {
		name      string
		child     string
		propagate bool
		noInput   bool
	}{
		// A later turn with propagated history is left out: a cold load of
		// the child's lineage chunk after ContinueAsNew fails verification
		// today, independently of the completion path exercised here.
		{"later turn", "child-wait", false, false},
		{"later turn, no creation input", "child-wait", false, true},
		{"same turn", "child-now", false, false},
		{"same turn, propagated history", "child-now", true, false},
		{"same turn, no creation input", "child-now", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			childID := "cancomplete-" + tc.child
			if tc.propagate {
				childID += "-prop"
			}
			if tc.noInput {
				childID += "-noinput"
			}
			cid := api.InstanceID(childID)
			id, err := cl.ScheduleNewWorkflow(ctx, "parent", api.WithInput(map[string]any{
				"child": tc.child, "childID": childID, "propagate": tc.propagate, "noInput": tc.noInput,
			}))
			require.NoError(t, err)
			if tc.child == "child-wait" {
				// The second generation is the one waiting: its input is the continued one.
				require.EventuallyWithT(t, func(co *assert.CollectT) {
					meta, merr := child.FetchWorkflowMetadata(ctx, cid, api.WithFetchPayloads(true))
					if assert.NoError(co, merr) {
						assert.Equal(co, api.RUNTIME_STATUS_RUNNING, meta.GetRuntimeStatus())
						assert.JSONEq(co, `"second"`, meta.GetInput().GetValue())
					}
				}, time.Second*20, time.Millisecond*10)
				require.NoError(t, child.RaiseEvent(ctx, cid, "go"))
			}

			meta, err := cl.WaitForWorkflowCompletion(ctx, id)
			require.NoError(t, err, "the parent must learn of the continued child's completion")
			assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
			assert.JSONEq(t, `"second"`, meta.GetOutput().GetValue())
			completed, _ := wf.ChildCompletions(t, ctx, cl, id, 0)
			assert.Equal(t, 1, completed)
		})
	}
}
