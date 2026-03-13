/*
Copyright 2026 The Dapr Authors
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

package resultreminder

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(restart))
}

type restart struct {
	workflow *workflow.Workflow
}

func (r *restart) Setup(t *testing.T) []framework.Option {
	config := `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: myconfig
spec:
  features:
  - name: WorkflowsRemoteActivityReminder
    enabled: true
`

	r.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, config)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, config)),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *restart) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	reg1 := dworkflow.NewRegistry()
	reg2 := dworkflow.NewRegistry()

	var inActivity atomic.Bool
	waitCh := make(chan struct{})
	reg1.AddWorkflowN("abc", func(ctx *dworkflow.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallActivity("def",
			dworkflow.WithActivityAppID(r.workflow.DaprN(1).AppID()),
		).Await(nil))
		return nil, nil
	})
	reg2.AddActivityN("def", func(ctx dworkflow.ActivityContext) (any, error) {
		inActivity.Store(true)
		<-waitCh
		return nil, nil
	})

	cctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)

	client1 := dworkflow.NewClient(r.workflow.DaprN(0).GRPCConn(t, cctx))
	client2 := dworkflow.NewClient(r.workflow.DaprN(1).GRPCConn(t, ctx))
	require.NoError(t, client1.StartWorker(cctx, reg1))
	require.NoError(t, client2.StartWorker(ctx, reg2))

	id, err := client1.ScheduleWorkflow(ctx, "abc")
	require.NoError(t, err)

	assert.Eventually(t, inActivity.Load, time.Second*10, time.Millisecond*10)

	cancel()
	r.workflow.DaprN(0).Cleanup(t)
	close(waitCh)

	appID := r.workflow.DaprN(0).AppID()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		list := r.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs/actorreminder")
		if !assert.Len(c, list, 1) {
			return
		}
		exp := "dapr/jobs/actorreminder||default||dapr.internal.default." + appID + ".workflow||" + id + "||"
		assert.Truef(c, strings.HasPrefix(list[0], exp), "reminder key should have correct prefid, expected prefix: %s, actual key: %s", exp, list[0])
	}, time.Second*20, time.Millisecond*10)

	r.workflow.DaprN(0).Restart(t, ctx)

	client1 = dworkflow.NewClient(r.workflow.DaprN(0).GRPCConn(t, ctx))
	require.NoError(t, client1.StartWorker(ctx, reg1))

	meta, err := client1.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	assert.Equal(t, dworkflow.StatusCompleted.String(), meta.RuntimeStatus.String())
}
