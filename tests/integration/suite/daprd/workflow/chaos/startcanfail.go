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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(startcanfail))
}

type startcanfail struct {
	workflow  *workflow.Workflow
	scheduler *scheduler.Scheduler
	proxy     *proxy.Proxy
}

func (s *startcanfail) Setup(t *testing.T) []framework.Option {
	appID := uuid.New().String()
	sen := sentry.New(t)
	s.scheduler = scheduler.New(t,
		scheduler.WithSentry(sen),
		scheduler.WithID("dapr-scheduler-server-0"),
	)
	s.proxy = proxy.New(t, s.scheduler, proxy.WithSentry(t, sen, "default", appID))

	s.workflow = workflow.New(t,
		workflow.WithSentryInstance(sen),
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID)),
		workflow.WithSchedulerInstance(s.scheduler),
		workflow.WithSchedulerAddress(s.proxy.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(sen, s.scheduler, s.proxy, s.workflow),
	}
}

func (s *startcanfail) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const wfID = "startcanfail-wf"
	const injected = 5

	r := s.workflow.Registry()
	require.NoError(t, r.AddWorkflowN("wf", func(octx *task.WorkflowContext) (any, error) {
		return "started", nil
	}))

	s.workflow.BackendClient(t, ctx)
	gclient := s.workflow.GRPCClient(t, ctx)

	failedCh := make(chan struct{})
	s.proxy.ArmFailures(proxy.MethodScheduleJob, injected, codes.Unknown, failedCh)

	_, err := gclient.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "wf",
		InstanceId:        wfID,
	})
	require.NoError(t, err,
		"the create must ride out transient Unknown ScheduleJob failures")

	select {
	case <-failedCh:
	case <-time.After(15 * time.Second):
		require.Fail(t, "injected ScheduleJob failure never fired")
	}

	assert.EventuallyWithT(t, func(co *assert.CollectT) {
		resp, gerr := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
			InstanceId:        wfID,
			WorkflowComponent: "dapr",
		})
		if assert.NoError(co, gerr) {
			assert.Equal(co, "COMPLETED", resp.GetRuntimeStatus())
		}
	}, 30*time.Second, 10*time.Millisecond)

	assert.Equal(t, injected, s.proxy.FailedCount(),
		"every armed failure must have been consumed by the create retry")
}
