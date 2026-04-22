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

package accesspolicy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(nopolicy))
}

// nopolicy tests that when no WorkflowAccessPolicy resources exist (but the
// feature flag is enabled), all cross-app workflow calls succeed (backward
// compatible behavior). Uses the same setup as the other accesspolicy tests
// to confirm the allow-all behavior is specifically due to the absence of
// policy resources.
type nopolicy struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	caller *daprd.Daprd
	target *daprd.Daprd
}

func (n *nopolicy) Setup(t *testing.T) []framework.Option {
	n.sentry = sentry.New(t)

	n.place = placement.New(t, placement.WithSentry(t, n.sentry))
	n.sched = scheduler.New(t, scheduler.WithSentry(n.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	n.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfaclconfig
spec:
  features:
  - name: WorkflowAccessPolicy
    enabled: true`), 0o600))

	commonOpts := []daprd.Option{
		daprd.WithNamespace("default"),
		daprd.WithConfigs(configFile),
		daprd.WithResourceFiles(n.db.GetComponent(t)),
		daprd.WithPlacementAddresses(n.place.Address()),
		daprd.WithSchedulerAddresses(n.sched.Address()),
		daprd.WithSentry(t, n.sentry),
	}
	n.caller = daprd.New(t, append(commonOpts, daprd.WithAppID("nopol-caller"))...)
	n.target = daprd.New(t, append(commonOpts, daprd.WithAppID("nopol-target"))...)

	return []framework.Option{
		framework.WithProcesses(n.sentry, n.place, n.sched, n.db, n.caller, n.target),
	}
}

func (n *nopolicy) Run(t *testing.T, ctx context.Context) {
	n.place.WaitUntilRunning(t, ctx)
	n.sched.WaitUntilRunning(t, ctx)
	n.caller.WaitUntilRunning(t, ctx)
	n.target.WaitUntilRunning(t, ctx)

	callerReg := task.NewTaskRegistry()
	targetReg := task.NewTaskRegistry()

	require.NoError(t, callerReg.AddWorkflowN("AnyWorkflow", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallActivity("AnyActivity", task.WithActivityAppID(n.target.AppID())).Await(&output)
		if err != nil {
			return nil, fmt.Errorf("activity failed: %w", err)
		}
		return output, nil
	}))
	require.NoError(t, targetReg.AddActivityN("AnyActivity", func(ctx task.ActivityContext) (any, error) {
		return "no-policy-ok", nil
	}))

	callerClient := client.NewTaskHubGrpcClient(n.caller.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, callerClient.StartWorkItemListener(ctx, callerReg))
	targetClient := client.NewTaskHubGrpcClient(n.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetReg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(n.caller.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(n.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	id, err := callerClient.ScheduleNewWorkflow(ctx, "AnyWorkflow")
	require.NoError(t, err)

	metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"no-policy-ok"`, metadata.GetOutput().GetValue())
}
