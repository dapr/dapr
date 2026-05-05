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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procworkflow "github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(activity))
}

// activity covers the activity-side receive verification path. Parent on App0
// schedules an activity on App1 with PropagateOwnHistory. App1's activity
// actor verifies the propagated chunk's signature and SPIFFE identity before
// scheduling the activity reminder. On verification failure, the activity
// never runs.
type activity struct {
	workflow *procworkflow.Workflow

	activityRan         atomic.Bool
	activityHistorySize atomic.Int64
}

func (s *activity) Setup(t *testing.T) []framework.Option {
	s.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(2),
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{framework.WithProcesses(s.workflow)}
}

func (s *activity) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	app0Reg := s.workflow.Registry()
	app1Reg := s.workflow.RegistryN(1)

	app1Reg.AddActivityN("signedRemoteAct", func(ctx task.ActivityContext) (any, error) {
		s.activityRan.Store(true)
		if ph := ctx.GetPropagatedHistory(); ph != nil {
			s.activityHistorySize.Store(int64(len(ph.Events())))
		}
		return "remote-done", nil
	})

	app0Reg.AddWorkflowN("signedActParent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallActivity("signedRemoteAct",
			task.WithActivityAppID(s.workflow.DaprN(1).AppID()),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	})

	client0 := s.workflow.BackendClient(t, ctx)
	s.workflow.BackendClientN(t, ctx, 1)

	id, err := client0.ScheduleNewWorkflow(ctx, "signedActParent")
	require.NoError(t, err)

	meta, err := client0.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(meta),
		"parent workflow must complete; signed activity propagation must verify on App1")

	assert.True(t, s.activityRan.Load(),
		"remote activity must run; verification failure would have blocked the work item")
	assert.Positive(t, s.activityHistorySize.Load(),
		"activity should observe the propagated history once verified")
}
