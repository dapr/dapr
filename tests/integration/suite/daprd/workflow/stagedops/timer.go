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

package stagedops

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow/stagedops"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(timer))
}

// timerDelay is how long the workflow's timer waits.
const timerDelay = time.Second * 6

// timer lets a running workflow's timer come due while no worker is
// connected.
//   - off: the workflow stays RUNNING; the scheduler holds the timer, and
//     the workflow completes once a worker connects.
//   - on:  the same.
type timer struct{ stagedops.Modes }

func (r *timer) Run(t *testing.T, ctx context.Context) {
	r.RunModes(t, ctx, func(t *testing.T, m stagedops.Mode) {
		require.NoError(t, m.WF.RegistryN(m.Idx(0)).AddWorkflowN("timer", func(ctx *task.WorkflowContext) (any, error) {
			return workflow.DoneOutput, ctx.CreateTimer(timerDelay).Await(nil)
		}))

		const id = api.InstanceID("timer")
		before := time.Now()
		m.StartWith(t, ctx, 0, "timer", stagedops.StatusRunning, id)
		started := time.Now()
		mgmt := m.Mgmt(t, ctx, 0)

		// StartWith returns once the worker is gone; that must be before the
		// timer is due, or this checks nothing. The timer is scheduled no
		// earlier than before.
		require.Less(t, started.Sub(before), timerDelay/2)

		// Past the timer, with a margin for it to fire.
		time.Sleep(time.Until(started.Add(timerDelay + time.Second*5)))
		assert.Equal(t, stagedops.StatusRunning, stagedops.StatusOf(ctx, mgmt, id))

		m.Connect(t, ctx, 0)
		stagedops.WaitStatus(t, ctx, mgmt, id, stagedops.StatusCompleted)
	})
}
