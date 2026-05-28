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
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/framework"
	fwos "github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(diskfull))
}

// diskfull fills the scheduler's backing disk (a size-capped tmpfs) with no
// workflow yet in progress. A Defragment call is issued via the scheduler's
// etcd client; the BoltDB rewrite cannot complete on a full disk and must
// return an error. The test then frees disk space, restarts the scheduler,
// schedules a workflow, and asserts it runs to completion. Self-wraps in an
// unprivileged user+mount namespace via `unshare`.
type diskfull struct {
	wf    *workflow.Workflow
	mount string
}

func (d *diskfull) Setup(t *testing.T) []framework.Option {
	if !fwos.InUnshareNamespace() {
		fwos.SkipUnlessUnshareAvailable(t)
		return nil
	}

	d.mount = fwos.MountTmpfs(t, 128)
	schedDataDir := filepath.Join(d.mount, "scheduler-data")
	require.NoError(t, os.MkdirAll(schedDataDir, 0o700))

	d.wf = workflow.New(t,
		workflow.WithSchedulerOptions(scheduler.WithDataDir(schedDataDir)),
		workflow.WithAddOrchestrator(t, "timerFlow", func(ctx *task.WorkflowContext) (any, error) {
			return nil, ctx.CreateTimer(time.Second).Await(nil)
		}),
	)
	return []framework.Option{framework.WithProcesses(d.wf)}
}

func (d *diskfull) Run(t *testing.T, ctx context.Context) {
	if fwos.ReexecInUserNamespace(t, ctx) {
		return
	}

	d.wf.WaitUntilRunning(t, ctx)

	padPath := filepath.Join(d.mount, "pad")
	fwos.FillDisk(t, padPath)

	sched := d.wf.Scheduler()
	defCtx, cancelDef := context.WithTimeout(ctx, 5*time.Second)
	_, err := sched.ETCDClient(t, ctx).Defragment(defCtx,
		"127.0.0.1:"+strconv.Itoa(sched.EtcdClientPort()))
	cancelDef()
	require.Error(t, err, "defragment must fail on full disk")

	require.NoError(t, os.Remove(padPath))

	sched.Restart(t, ctx)
	sched.WaitUntilRunning(t, ctx)

	cl := d.wf.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "timerFlow",
		api.WithInstanceID("wf-recovered"))
	require.NoError(t, err)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(meta))
}
