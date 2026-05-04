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

package config

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(idempotent))
}

// idempotent verifies that the retention reminder is created with a
// deterministic name and that the workflow runtime never accumulates duplicate
// retention reminders even when handleRetention is invoked from more than one
// code path. The orchestrator now calls handleRetention both in the completion
// path and in the empty-inbox early-exit path (defending against a prior run
// that lost its retention reminder Create to a transient scheduler failure).
// Both call sites must converge on a single reminder.
type idempotent struct {
	workflow *workflow.Workflow
}

func (i *idempotent) Setup(t *testing.T) []framework.Option {
	i.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfpolicy
spec:
  workflow:
    stateRetentionPolicy:
      anyTerminal: "168h"
`)),
	)

	return []framework.Option{
		framework.WithProcesses(i.workflow),
	}
}

func (i *idempotent) Run(t *testing.T, ctx context.Context) {
	i.workflow.WaitUntilRunning(t, ctx)

	const eventCount = 5

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("foo", func(ctx *dworkflow.WorkflowContext) (any, error) {
		for range eventCount {
			require.NoError(t, ctx.WaitForExternalEvent("evt", time.Minute).Await(nil))
		}
		return nil, nil
	})

	client := dworkflow.NewClient(i.workflow.Dapr().GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "foo")
	require.NoError(t, err)

	// Raise all events in parallel. When several events land in the inbox
	// before the orchestrator processes the first new-event reminder, the
	// first reminder drains the entire inbox (completing the workflow) and
	// the remaining queued reminders fire on a terminal workflow with an
	// empty inbox, exercising the new early-exit handleRetention path.
	var wg sync.WaitGroup
	for range eventCount {
		wg.Go(func() {
			assert.NoError(t, client.RaiseEvent(ctx, id, "evt"))
		})
	}
	wg.Wait()

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	// Allow any straggler new-event reminders to drain through the early-exit
	// path before asserting on scheduler state. Without this settle, a
	// still-firing reminder could be in flight when we sample.
	expectedKeyPrefix := fmt.Sprintf(
		"dapr/jobs/actorreminder||default||dapr.internal.default.%s.retentioner||%s||",
		i.workflow.Dapr().AppID(), id,
	)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		keys := i.workflow.Scheduler().ListAllKeys(t, ctx, expectedKeyPrefix)
		if !assert.Len(c, keys, 1, "expected exactly one retention reminder, got: %v", keys) {
			return
		}
		assert.Truef(c, strings.HasSuffix(keys[0], "||anyterminal"),
			"retention reminder name should be deterministic, got key: %s", keys[0])
	}, time.Second*10, time.Millisecond*10)
}
