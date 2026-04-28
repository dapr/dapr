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
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procworkflow "github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(workflowchain))
}

// Proves that when multiple wfs with the same name appear in a lineage chain
// GetWorkflowByName returns the last one
type workflowchain struct {
	workflow          *procworkflow.Workflow
	workerPluralCount atomic.Int32

	pluralFirstInstanceID atomic.Value
	pluralLastInstanceID  atomic.Value
	singularInstanceID    atomic.Value

	singularEqualsPluralLast atomic.Bool // cross-check
}

func (w *workflowchain) Setup(t *testing.T) []framework.Option {
	w.workflow = procworkflow.New(t,
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(w.workflow),
	}
}

func (w *workflowchain) Run(t *testing.T, ctx context.Context) {
	w.workflow.WaitUntilRunning(t, ctx)

	reg := w.workflow.Registry()
	reg.AddWorkflowN("worker", func(ctx *task.WorkflowContext) (any, error) {
		var depthStr string
		if err := ctx.GetInput(&depthStr); err != nil {
			return nil, err
		}
		depth, err := strconv.Atoi(depthStr)
		if err != nil {
			return nil, err
		}

		if depth < 3 {
			var next string
			if err := ctx.CallChildWorkflow("worker",
				task.WithChildWorkflowInput(strconv.Itoa(depth+1)),
				task.WithHistoryPropagation(api.PropagateLineage()),
			).Await(&next); err != nil {
				return nil, err
			}
			return next, nil
		}

		// depth == 3: look at propagated history
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return "no-history", nil
		}

		plural := ph.GetWorkflowsByName("worker")
		w.workerPluralCount.Store(int32(len(plural)))

		if len(plural) >= 1 {
			w.pluralFirstInstanceID.Store(plural[0].InstanceID)
		}
		if len(plural) >= 2 {
			w.pluralLastInstanceID.Store(plural[len(plural)-1].InstanceID)
		}

		singular, err := ph.GetWorkflowByName("worker")
		if err != nil {
			return nil, err
		}
		w.singularInstanceID.Store(singular.InstanceID)

		// Cross-check
		if len(plural) > 0 && singular.InstanceID == plural[len(plural)-1].InstanceID {
			w.singularEqualsPluralLast.Store(true)
		}

		return "done", nil
	})

	// parent kicks off the chain at depth=1 with lineage
	reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow("worker",
			task.WithChildWorkflowInput("1"),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	})

	client := w.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)

	metadata, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(metadata))

	// depth=3 sees 2 worker chunks in its lineage.
	// Its own chunk is not in its received history. This count
	// proves the 3-level chain executed. depth-3 couldn't observe 2 worker
	// ancestor chunks unless depths 1 & 2 both ran.
	require.Equal(t, int32(2), w.workerPluralCount.Load(),
		"GetWorkflowsByName('worker') should return 2 matches (depth-1 and depth-2)")

	first := w.pluralFirstInstanceID.Load()
	last := w.pluralLastInstanceID.Load()
	singular := w.singularInstanceID.Load()

	require.NotNil(t, first)
	require.NotNil(t, last)
	require.NotNil(t, singular)

	// The first and last must be distinct -diff instances
	assert.NotEqual(t, first, last, "depth-1 and depth-2 should have distinct instance IDs")
	assert.Equal(t, last, singular,
		"GetWorkflowByName('worker') should equal GetWorkflowsByName('worker')[len-1]")
	assert.True(t, w.singularEqualsPluralLast.Load(),
		"singular/plural consistency check should pass inside the examiner")
}
