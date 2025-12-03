/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwb.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package stalled

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow/stalled"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	suite.Register(new(patchorder))
}

type patchorder struct {
	waitingForEvent atomic.Bool

	fw *stalled.StalledFramework
}

func (r *patchorder) Setup(t *testing.T) []framework.Option {
	r.fw = stalled.NewStalledFramework()
	return r.fw.Setup(t)
}

func (r *patchorder) Run(t *testing.T, ctx context.Context) {
	r.fw.SetOldWorkflow(t, ctx, func(ctx *task.OrchestrationContext) (any, error) {
		ctx.IsPatched("patch1")
		ctx.IsPatched("patch2")
		if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})
	r.fw.SetNewWorkflow(t, ctx, func(ctx *task.OrchestrationContext) (any, error) {
		ctx.IsPatched("patch2")
		ctx.IsPatched("patch1")
		r.waitingForEvent.Store(true)
		if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})

	id := r.fw.ScheduleWorkflow(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, r.waitingForEvent.Load())
	}, 2*time.Second, 50*time.Millisecond)
	time.Sleep(500 * time.Millisecond)

	r.fw.KillCurrentReplica(t, ctx)
	r.fw.RunOldReplica(t, ctx)

	require.NoError(t, r.fw.CurrentClient.RaiseEvent(ctx, id, "Continue"))

	r.fw.WaitForStalled(t, ctx, id)
}
