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

package disk

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	fwos "github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(diskfullmidrun))
}

// diskfullmidrun mirrors the user-reported scenario against the actual-disk
// path: a workflow starts, progresses past the first step, then the scheduler's
// backing disk (a size-capped tmpfs) fills up mid-workflow. etcd's batch-tx
// commit fatal-exits the scheduler. The test then frees disk space, restarts
// the scheduler, and asserts the workflow resumes and completes. Self-wraps
// in an unprivileged user+mount namespace via `unshare`.
type diskfullmidrun struct {
	wf     *workflow.Workflow
	mount  string
	stepCh chan struct{}
}

func (d *diskfullmidrun) Setup(t *testing.T) []framework.Option {
	if !fwos.InUnshareNamespace() {
		fwos.SkipUnlessUnshareAvailable(t)
		return nil
	}

	d.mount = fwos.MountTmpfs(t, 128)
	d.stepCh = make(chan struct{}, 10)
	schedDataDir := filepath.Join(d.mount, "scheduler-data")
	require.NoError(t, os.MkdirAll(schedDataDir, 0o700))

	d.wf = workflow.New(t,
		workflow.WithSchedulerOptions(scheduler.WithDataDir(schedDataDir)),
		workflow.WithAddActivity(t, "step", func(ctx task.ActivityContext) (any, error) {
			select {
			case d.stepCh <- struct{}{}:
			default:
			}
			return nil, nil
		}),
		workflow.WithAddOrchestrator(t, "multiTimerFlow", func(ctx *task.WorkflowContext) (any, error) {
			for range 5 {
				if err := ctx.CreateTimer(time.Second).Await(nil); err != nil {
					return nil, err
				}
				if err := ctx.CallActivity("step").Await(nil); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}),
	)
	return []framework.Option{framework.WithProcesses(d.wf)}
}

func (d *diskfullmidrun) Run(t *testing.T, ctx context.Context) {
	if fwos.ReexecInUserNamespace(t, ctx) {
		return
	}

	d.wf.WaitUntilRunning(t, ctx)

	cl := d.wf.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "multiTimerFlow",
		api.WithInstanceID("wf-diskfull-midrun"))
	require.NoError(t, err)

	stepCtx, cancelStep := context.WithTimeout(ctx, 5*time.Second)
	defer cancelStep()
	select {
	case <-d.stepCh:
	case <-stepCtx.Done():
		t.Fatalf("workflow did not progress past first step: %v", stepCtx.Err())
	}

	padPath := filepath.Join(d.mount, "pad")
	fwos.FillDisk(t, padPath)

	sched := d.wf.Scheduler()

	sched.WaitUntilRunning(t, ctx)
	httpClient := client.HTTP(t)
	healthzURL := fmt.Sprintf("http://127.0.0.1:%d/healthz", sched.HealthzPort())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, healthzURL, nil)
		var resp *http.Response
		resp, err = httpClient.Do(req)
		if assert.Error(c, err) {
			return
		}
		resp.Body.Close()
	}, 10*time.Second, 50*time.Millisecond)

	require.NoError(t, os.Remove(padPath))

	sched.Restart(t, ctx)
	sched.WaitUntilRunning(t, ctx)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(meta))
}
