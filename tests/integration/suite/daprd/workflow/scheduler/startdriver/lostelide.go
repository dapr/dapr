/*
Copyright 2025 The Dapr Authors
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

package startdriver

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(lostelide))
}

// lostelide pins that a start backstop whose post-commit delete is lost fires
// into an empty inbox as a no-op: the completed workflow is not re-run and
// the one-shot is gone once it has fired.
type lostelide struct {
	workflow  *workflow.Workflow
	scheduler *scheduler.Scheduler
	proxy     *proxy.Proxy
	called    atomic.Int64
}

func (l *lostelide) Setup(t *testing.T) []framework.Option {
	appID := uuid.New().String()
	sen := sentry.New(t)
	l.scheduler = scheduler.New(t,
		scheduler.WithSentry(sen),
		scheduler.WithID("dapr-scheduler-server-0"),
	)
	l.proxy = proxy.New(t, l.scheduler, proxy.WithSentry(t, sen, "default", appID))
	l.workflow = workflow.New(t,
		workflow.WithFastPath(true),
		workflow.WithSentryInstance(sen),
		workflow.WithSchedulerInstance(l.scheduler),
		workflow.WithSchedulerAddress(l.proxy.Address()),
		workflow.WithDaprdOptions(0,
			daprd.WithAppID(appID),
			daprd.WithExecOptions(exec.WithEnvVars(t,
				"DAPR_WORKFLOW_JANITOR_PERIOD", "5m",
				"DAPR_WORKFLOW_PENDING_START_REDRIVE_GRACE", "1s",
			)),
		),
	)

	return []framework.Option{
		framework.WithProcesses(sen, l.scheduler, l.proxy, l.workflow),
	}
}

func (l *lostelide) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	// Every delete of the start reminder fails, so the backstop survives the
	// commit and fires one redrive grace after the start.
	deleteFailed := make(chan struct{})
	l.proxy.ArmNamedFailures(proxy.MethodDeleteJob, "start-es-", 1_000_000, codes.Unavailable, deleteFailed)
	t.Cleanup(func() { l.proxy.ArmFailures(proxy.MethodDeleteJob, 0, codes.Unavailable, nil) })

	require.NoError(t, l.workflow.Registry().AddWorkflowN("quick", func(*task.WorkflowContext) (any, error) {
		l.called.Add(1)
		return "done", nil
	}))
	cl := l.workflow.BackendClient(t, ctx)

	id, err := cl.ScheduleNewWorkflow(ctx, "quick")
	require.NoError(t, err)
	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())

	select {
	case <-deleteFailed:
	case <-time.After(time.Second * 10):
		require.Fail(t, "the start reminder elide never reached the scheduler")
	}

	// The backstop fires into the committed history: no second run, status
	// unchanged, and the fired one-shot is gone.
	assert.Never(t, func() bool { return l.called.Load() > 1 }, time.Second*3, time.Millisecond*10,
		"a stale start backstop re-ran a completed workflow")
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, l.scheduler.JobKeyCount(t, ctx, "||start-es-"))
	}, time.Second*10, time.Millisecond*10)
	meta, err = cl.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, int64(1), l.called.Load())
}
