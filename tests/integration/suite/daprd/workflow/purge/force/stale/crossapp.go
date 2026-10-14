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

package stale

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(crossapp))
}

// crossapp verifies a cross-app metadata read, which is served from the
// owning actor, does not report a force-purged instance as alive.
type crossapp struct {
	workflow *workflow.Workflow
}

func (s *crossapp) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *crossapp) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, s.workflow.Registry().AddWorkflowN("parked", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("go", time.Hour).Await(nil)
	}))
	owner := s.workflow.BackendClient(t, ctx)
	remote := s.workflow.BackendClientN(t, ctx, 1)
	ownerApp := s.workflow.Dapr().AppID()

	id, err := owner.ScheduleNewWorkflow(ctx, "parked")
	require.NoError(t, err)
	_, err = owner.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	meta, err := remote.FetchWorkflowMetadata(ctx, id, api.WithFetchAppID(ownerApp))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_RUNNING, meta.GetRuntimeStatus())

	require.NoError(t, owner.PurgeWorkflowState(ctx, id, api.WithForcePurge(true)))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, ferr := remote.FetchWorkflowMetadata(ctx, id, api.WithFetchAppID(ownerApp))
		assert.ErrorIs(c, ferr, api.ErrInstanceNotFound)
	}, time.Second*10, time.Millisecond*100)

	require.Error(t, remote.RaiseEvent(ctx, id, "go", api.WithRaiseEventAppID(ownerApp)))
}
