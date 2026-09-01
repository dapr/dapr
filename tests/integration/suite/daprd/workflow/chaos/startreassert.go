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

package chaos

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(startreassert))
}

type startreassert struct {
	workflow *workflow.Workflow
	sched    *scheduler.Scheduler
}

func (s *startreassert) Setup(t *testing.T) []framework.Option {
	s.sched = scheduler.New(t)
	s.workflow = workflow.New(t,
		workflow.WithSchedulerInstance(s.sched),
	)

	return []framework.Option{
		framework.WithProcesses(s.sched, s.workflow),
	}
}

func (s *startreassert) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const wfID = "startreassert-wf"

	r := s.workflow.Registry()
	require.NoError(t, r.AddWorkflowN("wf", func(octx *task.WorkflowContext) (any, error) {
		var in string
		if err := octx.GetInput(&in); err != nil {
			return nil, err
		}
		return "Hello, " + in + "!", nil
	}))

	client := s.workflow.BackendClient(t, ctx)

	startTime := time.Now().Add(10 * time.Second)
	id, err := client.ScheduleNewWorkflow(ctx, "wf",
		api.WithInstanceID(wfID),
		api.WithInput("Dapr"),
		api.WithStartTime(startTime),
	)
	require.NoError(t, err)

	ns := s.workflow.Dapr().Namespace()
	appID := s.workflow.Dapr().AppID()
	prefix := fmt.Sprintf(
		"dapr/jobs/actorreminder||%s||dapr.internal.%s.%s.workflow||%s||start-",
		ns, ns, appID, wfID,
	)

	keys := s.sched.ListAllKeys(t, ctx, prefix)
	require.Len(t, keys, 1, "expected exactly one start reminder after create")

	_, err = s.sched.ETCDClient(t, ctx).Delete(ctx, keys[0])
	require.NoError(t, err)
	assert.Empty(t, s.sched.ListAllKeys(t, ctx, prefix))

	_, err = client.ScheduleNewWorkflow(ctx, "wf",
		api.WithInstanceID(wfID),
		api.WithInput("Dapr"),
		api.WithStartTime(startTime),
	)
	require.NoError(t, err, "create retry must re-assert the missing start reminder")

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, s.sched.ListAllKeys(t, ctx, prefix), 1)
	}, 10*time.Second, 10*time.Millisecond)

	metadata, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"Hello, Dapr!"`, metadata.GetOutput().GetValue())
}
