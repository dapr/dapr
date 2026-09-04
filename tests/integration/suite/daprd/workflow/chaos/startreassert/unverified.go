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

package startreassert

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(unverified))
}

// unverified: the workflow API retries a create whose start
// reminder registration failed, and the retry re-asserts the reminder from
// the saved start after checking the Scheduler for it. That check must fail
// open: when the Scheduler cannot be asked, the retry re-asserts anyway.
// Previously the failed check aborted the retry, so the outage that lost the
// reminder also blocked its recovery.
type unverified struct {
	workflow  *workflow.Workflow
	scheduler *scheduler.Scheduler
	proxy     *proxy.Proxy
}

func (s *unverified) Setup(t *testing.T) []framework.Option {
	s.scheduler = scheduler.New(t)
	s.proxy = proxy.New(t, s.scheduler)
	s.workflow = workflow.New(t,
		workflow.WithSchedulerInstance(s.scheduler),
		workflow.WithSchedulerAddress(s.proxy.Address()),
		// Keep the status-read re-drive out of this test.
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_PENDING_START_REDRIVE_GRACE", "5m",
		))),
	)
	return []framework.Option{
		framework.WithProcesses(s.scheduler, s.proxy, s.workflow),
	}
}

func (s *unverified) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const wfID = "unverified-wf"
	require.NoError(t, s.workflow.Registry().AddWorkflowN("wf", func(*task.WorkflowContext) (any, error) {
		return "started", nil
	}))
	s.workflow.BackendClient(t, ctx)
	gclient := s.workflow.GRPCClient(t, ctx)

	// The first ScheduleJob fails permanently, so the state is committed
	// (save-first) with no reminder and no detached retry; every GetJob fails,
	// so the create retry cannot verify the reminder is missing.
	scheduleFailed := make(chan struct{})
	getFailed := make(chan struct{})
	s.proxy.ArmFailures(proxy.MethodScheduleJob, 1, codes.InvalidArgument, scheduleFailed)
	s.proxy.ArmFailures(proxy.MethodGetJob, 1_000_000, codes.Unavailable, getFailed)
	t.Cleanup(func() { s.proxy.ArmFailures(proxy.MethodGetJob, 0, codes.Unavailable, nil) })

	_, err := gclient.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "wf",
		InstanceId:        wfID,
	})
	require.NoError(t, err, "the create retry must re-assert the start reminder when its check is inconclusive")
	for name, ch := range map[string]chan struct{}{"ScheduleJob": scheduleFailed, "GetJob": getFailed} {
		select {
		case <-ch:
		case <-time.After(10 * time.Second):
			require.Failf(t, "injected failure never fired", "%s", name)
		}
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, gerr := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{InstanceId: wfID, WorkflowComponent: "dapr"})
		if assert.NoError(c, gerr) {
			assert.Equal(c, "COMPLETED", resp.GetRuntimeStatus())
		}
	}, 30*time.Second, 50*time.Millisecond)
}
