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

package chaos

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow/stagedops"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(savefailmetric))
}

// savefailmetric verifies that a turn whose save is refused is recorded as
// recoverable on the workflow execution metric, and that the terminal status
// (success, failed or terminated) is recorded exactly once, when the save
// finally commits. Continue-as-new generations record nothing.
type savefailmetric struct {
	workflow *workflow.Workflow
	store    *fault.Store
}

func (s *savefailmetric) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	s.store = fault.New(t)

	sock := socket.New(t)
	ss := statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(s.store),
	)

	s.workflow = workflow.New(t,
		// The armed store fault rolls back signed rows mid-commit, which signing
		// would classify as tampering instead of retrying.
		workflow.WithSigningDisabledN(0),
		workflow.WithNoDB(),
		workflow.WithDaprdOptions(0,
			daprd.WithSocket(t, sock),
			daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.%s
  version: v1
  metadata:
  - name: actorStateStore
    value: "true"
`, ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(ss, s.workflow),
	}
}

func (s *savefailmetric) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const failures = 3

	r := s.workflow.Registry()
	require.NoError(t, r.AddWorkflowN("succeed", func(*task.WorkflowContext) (any, error) {
		return nil, nil
	}))
	require.NoError(t, r.AddWorkflowN("fail", func(*task.WorkflowContext) (any, error) {
		return nil, errors.New("boom")
	}))
	require.NoError(t, r.AddWorkflowN("wait", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("never", -1).Await(nil)
	}))
	require.NoError(t, r.AddWorkflowN("can", func(ctx *task.WorkflowContext) (any, error) {
		var n int
		if err := ctx.GetInput(&n); err != nil {
			return nil, err
		}
		if n < 3 {
			ctx.ContinueAsNew(n + 1)
			return nil, nil
		}
		return n, nil
	}))
	cl := s.workflow.BackendClient(t, ctx)

	// Arms the next failures on the instance's history rows, which only a turn
	// commit writes: creation holds the start in the inbox.
	arm := func(t *testing.T, id string) {
		t.Helper()
		before := s.store.FailedCount()
		s.store.ArmFailures(id+"||history-", failures, nil)
		t.Cleanup(func() {
			assert.Equal(t, before+failures, s.store.FailedCount())
		})
	}

	tests := map[string]struct {
		name   string
		run    func(t *testing.T, id string)
		status string
		want   string
	}{
		"success": {
			name: "succeed",
			run: func(t *testing.T, id string) {
				arm(t, id)
				_, err := cl.ScheduleNewWorkflow(ctx, "succeed", api.WithInstanceID(api.InstanceID(id)))
				require.NoError(t, err)
			},
			status: "COMPLETED",
			want:   "success",
		},
		"failed": {
			name: "fail",
			run: func(t *testing.T, id string) {
				arm(t, id)
				_, err := cl.ScheduleNewWorkflow(ctx, "fail", api.WithInstanceID(api.InstanceID(id)))
				require.NoError(t, err)
			},
			status: "FAILED",
			want:   "failed",
		},
		"terminated": {
			name: "wait",
			run: func(t *testing.T, id string) {
				_, err := cl.ScheduleNewWorkflow(ctx, "wait", api.WithInstanceID(api.InstanceID(id)))
				require.NoError(t, err)
				_, err = cl.WaitForWorkflowStart(ctx, api.InstanceID(id))
				require.NoError(t, err)
				arm(t, id)
				require.NoError(t, cl.TerminateWorkflow(ctx, api.InstanceID(id)))
			},
			status: "TERMINATED",
			want:   "terminated",
		},
		"continue as new": {
			name: "can",
			run: func(t *testing.T, id string) {
				arm(t, id)
				_, err := cl.ScheduleNewWorkflow(ctx, "can", api.WithInstanceID(api.InstanceID(id)), api.WithInput(0))
				require.NoError(t, err)
			},
			status: "COMPLETED",
			want:   "success",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			id := "savefailmetric-" + tc.name
			tc.run(t, id)
			stagedops.WaitStatus(t, ctx, cl, api.InstanceID(id), tc.status)

			const metric = "dapr_runtime_workflow_execution_count"
			assert.EventuallyWithT(t, func(c *assert.CollectT) {
				m := s.workflow.Dapr().Metrics(c, ctx)
				for _, status := range []string{"success", "failed", "terminated"} {
					want := 0.0
					if status == tc.want {
						want = 1
					}
					assert.InDelta(c, want, m.SumWithLabels(metric, "status:"+status, "workflow_name:"+tc.name), 0, status)
				}
				assert.GreaterOrEqual(c, m.SumWithLabels(metric, "status:recoverable", "workflow_name:"+tc.name), float64(failures))
			}, 10*time.Second, 10*time.Millisecond)
		})
	}
}
