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

package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/log"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(redispatchstarve))
}

// redispatchstarve loses both of a workflow's activity executions, then holds
// every execution-claim read of the first one, so each janitor re-dispatch
// call for it uses up the whole pass's deadline. The second activity's call
// must not wait behind it: the first pass must re-dispatch it, and it must
// run.
type redispatchstarve struct {
	logOut   *log.Log
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (r *redispatchstarve) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	r.logOut = log.New()
	r.store = fault.New(t)
	var storeOpts []daprd.Option
	r.ss, storeOpts = workflow.FaultStore(t, r.store)
	r.workflow = workflow.New(t,
		workflow.WithNoDB(),
		workflow.WithDaprdOptions(0, append(storeOpts,
			daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
			daprd.WithWorkflowJanitorPeriod(t, time.Second*2),
			daprd.WithExecOptions(
				exec.WithEnvVars(t, "DAPR_WORKFLOW_TEST_DROP_ACTIVITY_DRIVES", "1000"),
				exec.WithStdout(r.logOut),
				exec.WithStderr(r.logOut),
			),
		)...),
	)

	return []framework.Option{
		framework.WithProcesses(r.ss, r.workflow),
	}
}

func (r *redispatchstarve) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	const id = "redispatchstarve"
	var aRuns, bRuns atomic.Int32
	require.NoError(t, r.workflow.Registry().AddWorkflowN("Starve", func(c *task.WorkflowContext) (any, error) {
		a, b := c.CallActivity("A"), c.CallActivity("B")
		if err := a.Await(nil); err != nil {
			return nil, err
		}
		return nil, b.Await(nil)
	}))
	require.NoError(t, r.workflow.Registry().AddActivityN("A", func(task.ActivityContext) (any, error) {
		aRuns.Add(1)
		return nil, nil
	}))
	require.NoError(t, r.workflow.Registry().AddActivityN("B", func(task.ActivityContext) (any, error) {
		bRuns.Add(1)
		return nil, nil
	}))
	client := r.workflow.BackendClient(t, ctx)

	// Hold each claim read of A until its call's deadline ends it, and arm
	// the next one at once.
	stop := make(chan struct{})
	held := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
	})
	go func() {
		defer close(held)
		var releases []func()
		defer func() {
			for _, release := range releases {
				release()
			}
		}()
		for {
			arrived, release := r.store.ArmGetHold(id + "::0::0||execution-claim")
			releases = append(releases, release)
			select {
			case <-arrived:
			case <-stop:
				return
			}
		}
	}()

	_, err := client.ScheduleNewWorkflow(ctx, "Starve", api.WithInstanceID(id))
	require.NoError(t, err)

	assert.Eventually(t, func() bool { return bRuns.Load() > 0 }, time.Second*20, time.Millisecond*10,
		"B must run while A's call uses up every pass's deadline")
	// A task is recorded as re-dispatched before its call runs, so a call
	// that never reached B makes the next pass escalate it instead, and this
	// line is never logged.
	assert.True(t, r.logOut.Contains("janitor re-dispatched unresolved activity 'B::1'"),
		"B's re-dispatch must not wait behind A's: a call that starts after the deadline never reaches B")
	assert.Zero(t, aRuns.Load(), "A's re-dispatch must still be held")

	close(stop)
	<-held
	meta, err := client.WaitForWorkflowCompletion(ctx, api.InstanceID(id))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
}
