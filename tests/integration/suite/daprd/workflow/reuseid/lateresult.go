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

package reuseid

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(lateresult))
}

// lateresult completes a workflow while one of its activities is still
// running. The late result reaches a completed workflow: it is acked and
// dropped, leaving no inbox row and no wake-up, and the instance ID is
// reusable without a purge.
type lateresult struct {
	workflow *workflow.Workflow
	logline  *logline.LogLine
}

func (l *lateresult) Setup(t *testing.T) []framework.Option {
	l.logline = logline.New(t, logline.WithCaptureAll())
	l.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0,
			daprd.WithExecOptions(exec.WithStdout(l.logline.Stdout()), exec.WithStderr(l.logline.Stderr())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(l.logline, l.workflow),
	}
}

func (l *lateresult) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	const id = api.InstanceID("reuse-lateresult")
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)

	reg := l.workflow.Registry()
	require.NoError(t, reg.AddActivityN("slow", func(task.ActivityContext) (any, error) {
		<-release
		return "late", nil
	}))
	require.NoError(t, reg.AddWorkflowN("lateresult", func(wctx *task.WorkflowContext) (any, error) {
		// Scheduled and never awaited: the workflow completes with it running.
		wctx.CallActivity("slow")
		return "done", nil
	}))

	client := l.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "lateresult", api.WithInstanceID(id))
	require.NoError(t, err)
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())

	// The result's admission is logged under the actor lock on either path:
	// the durable inbox add, or the drop.
	admitted := func() int {
		n := 0
		for _, line := range []string{"adding event to the workflow inbox", "dropping completion (sender"} {
			n += bytes.Count(l.logline.StdoutBuffer(), fmt.Appendf(nil, "Workflow actor '%s': %s", id, line))
		}
		return n
	}
	releaseOnce()
	require.Eventually(t, func() bool { return admitted() > 0 }, time.Second*20, time.Millisecond*10,
		"the late result must reach the workflow actor")

	ns, appID := l.workflow.Dapr().Namespace(), l.workflow.Dapr().AppID()
	zero := func(n int) bool { return n == 0 }
	l.workflow.Scheduler().WaitJobKeyCount(t, ctx, fmt.Sprintf("||dapr.internal.%s.%s.workflow||%s||", ns, appID, id), zero)
	l.workflow.Scheduler().WaitJobKeyCount(t, ctx, fmt.Sprintf("||dapr.internal.%s.%s.activity||%s::", ns, appID, id), zero)
	hist, err := client.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	for _, e := range hist.GetEvents() {
		assert.Nil(t, e.GetTaskCompleted(), "the late result must be dropped, not applied")
	}

	// Terminal instances are reusable by default: the dropped result must
	// have settled the await, or the create is refused.
	_, err = client.ScheduleNewWorkflow(ctx, "lateresult", api.WithInstanceID(id))
	require.NoError(t, err, "reusing the ID after the late result must succeed")
	meta, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
}
