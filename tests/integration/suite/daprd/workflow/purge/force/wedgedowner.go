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

package force

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(wedgedowner))
}

// wedgedowner force purges a running workflow through the daprd that does not
// host its actor while the owner is inside a turn that outlives the purge's
// eviction budget. The eviction does not land, the purge deletes the rows and
// reminders, and the owner's turn then fails to save: its actor stays
// resident with no cache. A start of the same ID through the owner must begin
// as a new instance: on the fast path that shows as the new instance arming
// its own janitor reminder, which the purged instance's had been deleted.
type wedgedowner struct {
	workflow *workflow.Workflow
}

func (w *wedgedowner) Setup(t *testing.T) []framework.Option {
	appID := uuid.NewString()
	w.workflow = workflow.New(t,
		workflow.WithFastPath(true),
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithAppID(appID)),
	)

	return []framework.Option{
		framework.WithProcesses(w.workflow),
	}
}

func (w *wedgedowner) Run(t *testing.T, ctx context.Context) {
	w.workflow.WaitUntilRunning(t, ctx)

	var entered sync.Once
	inTurn := make(chan struct{})
	hold := make(chan struct{})
	reg := dworkflow.NewRegistry()
	reg.AddActivityN("noop", func(dworkflow.ActivityContext) (any, error) { return nil, nil })
	// The second turn, after the activity completes, holds the turn lock
	// until the test releases it.
	reg.AddWorkflowN("wedge", func(ctx *dworkflow.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("noop").Await(nil); err != nil {
			return nil, err
		}
		entered.Do(func() { close(inTurn) })
		<-hold
		return nil, nil
	})
	reg.AddWorkflowN("wait", func(ctx *dworkflow.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("noop").Await(nil); err != nil {
			return nil, err
		}
		return nil, ctx.WaitForExternalEvent("go", time.Hour).Await(nil)
	})

	clients := []*dworkflow.Client{
		w.workflow.WorkflowClientN(t, ctx, 0),
		w.workflow.WorkflowClientN(t, ctx, 1),
	}
	for _, c := range clients {
		require.NoError(t, c.StartWorker(ctx, reg))
	}

	janitors := func() int {
		return w.workflow.Scheduler().JobKeyCount(t, ctx, "new-event-janitor")
	}
	// Polls run on another goroutine: they look the keys up through a
	// collector-aware lookup dialled here, on the test goroutine.
	jobKeys := w.workflow.Scheduler().JobKeysLookup(t, ctx)

	const id = "wedgedowner"
	_, err := clients[0].ScheduleWorkflow(ctx, "wedge", dworkflow.WithInstanceID(id))
	require.NoError(t, err)
	select {
	case <-inTurn:
	case <-ctx.Done():
		require.Fail(t, "the wedge turn did not start")
	}
	owner := w.workflow.OwnerIndex(t, ctx, 0, 1)
	require.Equal(t, 1, janitors(), "the first instance armed its janitor on the fast path")

	// The eviction waits for the turn lock until its budget runs out, then
	// the purge goes ahead without it.
	require.NoError(t, clients[1-owner].PurgeWorkflowState(ctx, id, dworkflow.WithForcePurge(true)))
	assert.Zero(t, janitors(), "the purge deleted the first instance's janitor")
	close(hold)

	// The owner's turn commits into deleted rows and fails; its actor stays
	// resident. The new instance under the same ID must start clean and arm
	// its own janitor.
	_, err = clients[owner].ScheduleWorkflow(ctx, "wait", dworkflow.WithInstanceID(id))
	require.NoError(t, err)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, jobKeys(c, "new-event-janitor"), 1)
	}, time.Second*10, time.Millisecond*10, "the new instance must arm its own janitor")
}
