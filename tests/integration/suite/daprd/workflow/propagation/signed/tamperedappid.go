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
	suite.Register(new(tamperedappid))
}

// tamperedappid verifies that flipping a chunk's claimed AppId so it no longer
// matches the leaf cert's SPIFFE identity is caught by the load-time SPIFFE-ID
// check. The receiver tombstones the workflow.
type tamperedappid struct {
	workflow *procworkflow.Workflow

	childInstanceID atomic.Value // string
}

func (s *tamperedappid) Setup(t *testing.T) []framework.Option {
	s.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(2),
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{framework.WithProcesses(s.workflow)}
}

func (s *tamperedappid) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	app0Reg := s.workflow.Registry()
	app1Reg := s.workflow.RegistryN(1)

	app1Reg.AddWorkflowN("appidChild", func(ctx *task.WorkflowContext) (any, error) {
		s.childInstanceID.Store(string(ctx.ID))
		var p string
		if err := ctx.WaitForSingleEvent("continue", 30*time.Second).Await(&p); err != nil {
			return nil, err
		}
		return p, nil
	})

	app0Reg.AddWorkflowN("appidParent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow("appidChild",
			task.WithChildWorkflowAppID(s.workflow.DaprN(1).AppID()),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	})

	client0 := s.workflow.BackendClient(t, ctx)
	client1 := s.workflow.BackendClientN(t, ctx, 1)

	_, err := client0.ScheduleNewWorkflow(ctx, "appidParent")
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

	// Rewrite the chunk's AppId to a value that does not match the
	// signing cert's SPIFFE identity.
	key, ph := fworkflow.ReadPropagatedHistory(t, ctx, s.workflow.DB(), childID)
	require.NotEmpty(t, ph.GetChunks())
	originalAppID := ph.GetChunks()[0].GetAppId()
	ph.GetChunks()[0].AppId = "spoofed-app-name"
	fworkflow.WritePropagatedHistory(t, ctx, s.workflow.DB(), key, ph)
	require.NotEqual(t, "spoofed-app-name", originalAppID,
		"sanity: original chunk should not have been pre-spoofed")

	s.workflow.DaprN(1).Restart(t, ctx)
	s.workflow.DaprN(1).WaitUntilRunning(t, ctx)
	client1 = s.workflow.BackendClientN(t, ctx, 1)

	require.NoError(t, client1.RaiseEvent(ctx, api.InstanceID(childID), "continue", api.WithEventPayload("real-event")))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, err := client1.FetchWorkflowMetadata(ctx, api.InstanceID(childID))
		assert.NoError(c, err)
		assert.Equal(c, api.RUNTIME_STATUS_FAILED, meta.GetRuntimeStatus(),
			"chunk-appid mismatch must tombstone the child workflow")
	}, 20*time.Second, 10*time.Millisecond)
}
