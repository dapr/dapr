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

package config

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(notexists))
}

type notexists struct {
	workflow *workflow.Workflow
}

func (n *notexists) Setup(t *testing.T) []framework.Option {
	n.workflow = workflow.New(t,
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
		framework.WithProcesses(n.workflow),
	}
}

func (n *notexists) Run(t *testing.T, ctx context.Context) {
	n.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("foo", func(ctx *dworkflow.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallActivity("abc").Await(nil))
		return nil, nil
	})
	reg.AddActivityN("abc", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, nil
	})

	client := dworkflow.NewClient(n.workflow.Dapr().GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, n.workflow.Dapr().GetMetaActorRuntime(t, ctx).ActiveActors, 3)
	}, time.Second*10, time.Millisecond*10)

	// Inject the retentioner reminder via the scheduler directly. The daprd
	// RegisterActorReminder API rejects "dapr.internal.*" actor types because
	// they are reserved for the workflow runtime.
	dueTime := time.Now().Add(3 * time.Second).Format(time.RFC3339)
	appID := n.workflow.Dapr().AppID()
	_, err := n.workflow.Scheduler().Client(t, ctx).ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name: "anyterminal-dxnUithe",
		Job:  &schedulerv1pb.Job{DueTime: &dueTime},
		Metadata: &schedulerv1pb.JobMetadata{
			Namespace: "default",
			AppId:     appID,
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{
						Type: "dapr.internal.default." + appID + ".retentioner",
						Id:   "helloworld",
					},
				},
			},
		},
	})
	require.NoError(t, err)

	assert.Len(t, n.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs/actorreminder||default||dapr.internal."), 1)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, n.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs/actorreminder||default||dapr.internal."))
	}, time.Second*10, time.Millisecond*10)
}
