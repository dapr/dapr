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

package propagation

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procworkflow "github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(activityretry))
}

// activityretry verifies propagated history behavior when an activity fails
// and is retried by the SDK retry policy

// Expected behavior under retry:
//   - Each retry emits a separate ScheduleTask action with a new event ID
//   - Parent's history therefore records both attempts
//   - GetActivitiesByName returns both attempts, GetActivityByName returns the last attempt
type activityretry struct {
	workflow *procworkflow.Workflow

	activityAttempts atomic.Int64

	childHistoryReceived atomic.Bool

	// GetWorkflowByName
	parentFound          atomic.Bool
	nonexistentWFFound   atomic.Bool
	parentWFName         atomic.Value
	parentWFInstanceSeen atomic.Bool

	// GetWorkflowsByName
	parentWFsCount      atomic.Int64
	nonexistentWFsIsNil atomic.Bool

	// GetActivityByName
	singularStarted             atomic.Bool
	singularCompleted           atomic.Bool
	singularFailed              atomic.Bool
	nonexistentActSeenAsStarted atomic.Bool

	// GetActivitiesByName
	pluralCount          atomic.Int64
	firstAttemptFailed   atomic.Bool
	secondAttemptPassed  atomic.Bool
	nonexistentActsIsNil atomic.Bool

	singularEqualsPluralLast atomic.Bool
}

func (a *activityretry) Setup(t *testing.T) []framework.Option {
	a.workflow = procworkflow.New(t,
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *activityretry) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	reg := a.workflow.Registry()

	reg.AddActivityN("CallLLM", func(ctx task.ActivityContext) (any, error) {
		n := a.activityAttempts.Add(1)
		// fails on attempt 1
		if n == 1 {
			return nil, errors.New("transient LLM error")
		}
		// succeeds on attempt 2
		return "llm-ok", nil
	})

	reg.AddWorkflowN("parentWf", func(ctx *task.WorkflowContext) (any, error) {
		var llmResult string
		if err := ctx.CallActivity("CallLLM",
			task.WithActivityRetryPolicy(&task.RetryPolicy{
				MaxAttempts:          2,
				InitialRetryInterval: 10 * time.Millisecond,
			}),
		).Await(&llmResult); err != nil {
			return nil, err
		}

		var childResult string
		if err := ctx.CallChildWorkflow("childWf",
			task.WithChildWorkflowInput("go"),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&childResult); err != nil {
			return nil, err
		}
		return childResult, nil
	})

	// check propagated history
	reg.AddWorkflowN("childWf", func(ctx *task.WorkflowContext) (any, error) {
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return statusNoHistoryHyphen, nil
		}
		a.childHistoryReceived.Store(true)

		parent, parentErr := ph.GetWorkflowByName("parentWf")
		if parentErr == nil {
			a.parentFound.Store(true)
			a.parentWFName.Store(parent.Name)
			if parent.InstanceID != "" {
				a.parentWFInstanceSeen.Store(true)
			}
		}
		if _, err := ph.GetWorkflowByName("DoesNotExist"); err == nil {
			a.nonexistentWFFound.Store(true) // should stay false
		}

		parentWFs := ph.GetWorkflowsByName("parentWf")
		a.parentWFsCount.Store(int64(len(parentWFs)))
		nonexistentWFs := ph.GetWorkflowsByName("DoesNotExist")
		if nonexistentWFs == nil {
			a.nonexistentWFsIsNil.Store(true)
		}

		if parentErr != nil {
			//nolint:nilerr
			return "no-parent", nil
		}

		singular, _ := parent.GetActivityByName("CallLLM")
		if singular.Started {
			a.singularStarted.Store(true)
		}
		if singular.Completed {
			a.singularCompleted.Store(true)
		}
		if singular.Failed {
			a.singularFailed.Store(true)
		}
		if nonexistentAct, err := parent.GetActivityByName("NotAnActivity"); err == nil && nonexistentAct.Started {
			a.nonexistentActSeenAsStarted.Store(true) // should stay false
		}

		all := parent.GetActivitiesByName("CallLLM")
		a.pluralCount.Store(int64(len(all)))
		if len(all) >= 1 && all[0].Failed {
			a.firstAttemptFailed.Store(true)
		}
		if len(all) >= 2 && all[1].Completed {
			a.secondAttemptPassed.Store(true)
		}
		nonexistentActs := parent.GetActivitiesByName("NotAnActivity")
		if nonexistentActs == nil {
			a.nonexistentActsIsNil.Store(true)
		}

		// Cross-check: singular == plural[len-1]
		if len(all) > 0 {
			last := all[len(all)-1]
			if singular.Started == last.Started &&
				singular.Completed == last.Completed &&
				singular.Failed == last.Failed &&
				singular.Output.GetValue() == last.Output.GetValue() {
				a.singularEqualsPluralLast.Store(true)
			}
		}

		return statusDone, nil
	})

	client := a.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "parentWf")
	require.NoError(t, err)

	metadata, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))

	// Activity was invoked twice: one failure, one success.
	assert.Equal(t, int64(2), a.activityAttempts.Load(), "CallLLM should have been invoked twice (1 fail + 1 succeed)")
	require.True(t, a.childHistoryReceived.Load(), "child should have received propagated history with lineage")

	// GetWorkflowByName
	assert.True(t, a.parentFound.Load(), "GetWorkflowByName('parentWf') should be Found")
	assert.Equal(t, "parentWf", a.parentWFName.Load(), "GetWorkflowByName should expose the workflow name")
	assert.True(t, a.parentWFInstanceSeen.Load(), "GetWorkflowByName should expose the workflow instance ID")
	assert.False(t, a.nonexistentWFFound.Load(), "GetWorkflowByName('DoesNotExist') should NOT be Found")

	// GetWorkflowsByName
	assert.Equal(t, int64(1), a.parentWFsCount.Load(), "GetWorkflowsByName('parentWf') should return one match")
	assert.True(t, a.nonexistentWFsIsNil.Load(), "GetWorkflowsByName('DoesNotExist') should return nil")

	// GetActivityByName: returns last attempt successful
	assert.True(t, a.singularStarted.Load(), "GetActivityByName should see the last attempt as Started")
	assert.True(t, a.singularCompleted.Load(), "GetActivityByName should return the latest attempt (Completed=true)")
	assert.False(t, a.singularFailed.Load(), "GetActivityByName should NOT be the failed first attempt")
	assert.False(t, a.nonexistentActSeenAsStarted.Load(), "GetActivityByName('NotAnActivity') should have Started=false")

	// GetActivitiesByName: returns BOTH attempts in order
	assert.Equal(t, int64(2), a.pluralCount.Load(), "GetActivitiesByName should return both retry attempts")
	assert.True(t, a.firstAttemptFailed.Load(), "first entry should be the failed attempt")
	assert.True(t, a.secondAttemptPassed.Load(), "second entry should be the successful attempt")
	assert.True(t, a.nonexistentActsIsNil.Load(), "GetActivitiesByName('NotAnActivity') should return nil")

	// Cross-check: singular == plural[len-1]
	assert.True(t, a.singularEqualsPluralLast.Load(),
		"GetActivityByName should return the same ActivityResult as GetActivitiesByName's last entry")
}
