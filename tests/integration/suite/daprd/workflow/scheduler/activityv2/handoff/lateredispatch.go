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

package handoff

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(lateredispatch))
}

// lateredispatchPeriod is the janitor period, and so also the floor of the
// execution-claim record's retention. It is long enough that the first
// recovery arrival on a moved activity's new owner is a janitor re-dispatch,
// not a run-activity reminder from an escalation before the move.
const lateredispatchPeriod = 5 * time.Second

// lateredispatch holds a janitor re-dispatch on a moved activity's new owner
// while it reads the execution-claim record, lets the old owner finish and
// the completed record expire, then lets the read through. A re-dispatch call
// must not outlive one janitor period, the record's retention floor: a call
// still alive at that point finds no record and runs the body again.
type lateredispatch struct {
	workflow *workflow.Workflow
	store    *fault.Store
	joiners  [2]*daprd.Daprd
	churners [3]*daprd.Daprd
}

func (l *lateredispatch) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	l.store = fault.New(t)
	ss, storeOpts := workflow.FaultStore(t, l.store)

	opts := append([]daprd.Option{
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
		daprd.WithWorkflowJanitorPeriod(t, lateredispatchPeriod),
		daprd.WithWorkflowClaimRetention(t, time.Millisecond),
	}, storeOpts...)
	l.workflow = workflow.New(t,
		workflow.WithNoDB(),
		workflow.WithDaprdOptions(0, opts...),
	)

	newDaprd := func() *daprd.Daprd {
		dopts := []daprd.Option{
			daprd.WithAppID(l.workflow.Dapr().AppID()),
			daprd.WithSchedulerAddresses(l.workflow.Scheduler().Address()),
		}
		if l.workflow.HasPlacement() {
			dopts = append(dopts, daprd.WithPlacementAddresses(l.workflow.Placement().Address()))
		}
		return daprd.New(t, append(dopts, append(opts, l.workflow.JoinOptions(t)...)...)...)
	}
	for i := range l.joiners {
		l.joiners[i] = newDaprd()
	}
	for i := range l.churners {
		l.churners[i] = newDaprd()
	}

	return []framework.Option{
		framework.WithProcesses(ss, l.workflow),
	}
}

func (l *lateredispatch) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)
	claims := wf.TrackClaimRecords(l.store)

	const batch = 6

	var executions atomic.Int64
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseBodies := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseBodies)

	wfFn := func(c *task.WorkflowContext) (any, error) {
		var out string
		if err := c.CallActivity("Slow").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}
	actFn := func(c task.ActivityContext) (any, error) {
		executions.Add(1)
		select {
		case <-release:
			return "late-done", nil
		case <-c.Context().Done():
			return nil, c.Context().Err()
		}
	}
	registry := func() *task.TaskRegistry {
		r := task.NewTaskRegistry()
		require.NoError(t, r.AddWorkflowN("LateRedispatch", wfFn))
		require.NoError(t, r.AddActivityN("Slow", actFn))
		return r
	}

	require.NoError(t, l.workflow.Registry().AddWorkflowN("LateRedispatch", wfFn))
	require.NoError(t, l.workflow.Registry().AddActivityN("Slow", actFn))
	client := l.workflow.BackendClient(t, ctx)

	var ids []string
	start := func() {
		t.Helper()
		for range batch {
			resp, err := l.workflow.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
				WorkflowComponent: "dapr",
				WorkflowName:      "LateRedispatch",
			})
			require.NoError(t, err)
			ids = append(ids, resp.GetInstanceId())
		}
		require.Eventually(t, func() bool {
			return executions.Load() >= int64(len(ids))
		}, time.Second*30, time.Millisecond*10,
			"every scheduled activity body must be mid-execution before the churn")
	}
	start()

	for _, joiner := range l.joiners {
		l.workflow.Join(t, ctx, joiner, registry())
	}
	extends := make([]func(), 0, len(l.churners))
	for _, churner := range l.churners {
		extends = append(extends, func() {
			start()
			l.workflow.Join(t, ctx, churner, registry())
		})
	}
	wf.EnsureClaimRecordsBy(t, ctx, claims, extends)

	arrived, releaseRead := l.store.ArmGetHold("||execution-claim")
	t.Cleanup(releaseRead)
	select {
	case <-arrived:
	case <-time.After(lateredispatchPeriod * 3):
		require.Fail(t, "no janitor re-dispatch read a moved activity's execution-claim record")
	}

	releaseBodies()
	for _, id := range ids {
		metadata, err := client.WaitForWorkflowCompletion(ctx, api.InstanceID(id))
		require.NoError(t, err)
		assert.Equal(t, `"late-done"`, metadata.GetOutput().GetValue())
	}

	require.Eventually(t, func() bool { return claims() == 0 },
		lateredispatchPeriod*4, time.Millisecond*50,
		"the completed execution-claim records must expire after their retention")
	before := executions.Load()
	require.Equal(t, int64(len(ids)), before, "a body ran twice before the held read was released")
	releaseRead()

	assert.Never(t, func() bool { return executions.Load() > before },
		time.Second*3, time.Millisecond*10,
		"a janitor re-dispatch that outlived the claim record's retention ran the body again")
}
