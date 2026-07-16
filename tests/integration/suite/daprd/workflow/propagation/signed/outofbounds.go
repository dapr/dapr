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

package signed

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procworkflow "github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(outofbounds))
}

// outofbounds verifies that truncating a chunk's rawEvents (removing entries
// the producer actually signed) is rejected. Counterpart to extrarawevents:
// extrarawevents extends past what was signed; this trims short of it.
// EventsDigest is length-prefixed, so adding or removing entries both
// invalidate the chunk's signature.
type outofbounds struct {
	workflow *procworkflow.Workflow

	childInstanceID atomic.Value // string
}

func (s *outofbounds) Setup(t *testing.T) []framework.Option {
	s.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(2),
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{framework.WithProcesses(s.workflow)}
}

func (s *outofbounds) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	app0Reg := s.workflow.Registry()
	app1Reg := s.workflow.RegistryN(1)

	app1Reg.AddWorkflowN("oobChild", func(ctx *task.WorkflowContext) (any, error) {
		s.childInstanceID.Store(string(ctx.ID))
		var p string
		if err := ctx.WaitForSingleEvent("continue", 30*time.Second).Await(&p); err != nil {
			return nil, err
		}
		return p, nil
	})

	app0Reg.AddWorkflowN("oobParent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		return out, ctx.CallChildWorkflow("oobChild",
			task.WithChildWorkflowAppID(s.workflow.DaprN(1).AppID()),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&out)
	})

	client0 := s.workflow.BackendClient(t, ctx)
	client1 := s.workflow.BackendClientN(t, ctx, 1)

	_, err := client0.ScheduleNewWorkflow(ctx, "oobParent")
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		v := s.childInstanceID.Load()
		assert.NotNil(c, v)
		if v != nil {
			assert.NotEmpty(c, v.(string))
		}
	}, 20*time.Second, 10*time.Millisecond)
	childID := s.childInstanceID.Load().(string)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, fworkflow.CountPropagatedHistoryRows(t, ctx, s.workflow.DB(), childID))
	}, 20*time.Second, 10*time.Millisecond)

	_, err = client1.WaitForWorkflowStart(ctx, api.InstanceID(childID))
	require.NoError(t, err)

	// Drop the chunk's last rawEvent. The chunk's signature was over the
	// original count of entries; removing one shifts EventsDigest and the
	// signature no longer verifies.
	key, ph := fworkflow.ReadPropagatedHistory(t, ctx, s.workflow.DB(), childID)
	require.NotEmpty(t, ph.GetChunks())
	require.GreaterOrEqual(t, len(ph.GetChunks()[0].GetRawEvents()), 2,
		"sanity: chunk should have at least two rawEvents so we can drop one and still have a non-empty chunk")
	chunk := ph.GetChunks()[0]
	chunk.RawEvents = chunk.GetRawEvents()[:len(chunk.GetRawEvents())-1]
	fworkflow.WritePropagatedHistory(t, ctx, s.workflow.DB(), key, ph)

	s.workflow.DaprN(1).Restart(t, ctx)
	s.workflow.DaprN(1).WaitUntilRunning(t, ctx)
	client1 = s.workflow.BackendClientN(t, ctx, 1)

	require.NoError(t, client1.RaiseEvent(ctx, api.InstanceID(childID), "continue", api.WithEventPayload("real-event")))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, err := client1.FetchWorkflowMetadata(ctx, api.InstanceID(childID))
		assert.NoError(c, err)
		assert.Equal(c, api.RUNTIME_STATUS_FAILED, meta.GetRuntimeStatus(),
			"truncating a chunk's rawEvents must tombstone the child")
	}, 20*time.Second, 10*time.Millisecond)
}
