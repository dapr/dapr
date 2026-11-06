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

package stagedops

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(crossappoff))
	suite.Register(new(crossappon))
}

type (
	crossappoff struct{ crossapp }
	crossappon  struct{ crossapp }
)

func (c *crossappon) Setup(t *testing.T) []framework.Option {
	c.staged = true
	return c.crossapp.Setup(t)
}

// crossapp calls, from app X which has a worker, each operation on workflows
// of app Y which has none.
//   - off: get status, start, raise, purge and recursive force purge all
//     fail, no worker connected.
//   - on:  all are accepted.
//
// A force purge without recursive fails in both modes: a cross-app force
// purge must be recursive.
type crossapp struct{ mode }

func (c *crossapp) Setup(t *testing.T) []framework.Option {
	c.wf = newWorkflow(t, c.staged, 2)
	return []framework.Option{framework.WithProcesses(c.wf)}
}

func (c *crossapp) Run(t *testing.T, ctx context.Context) {
	c.wf.WaitUntilRunning(t, ctx)
	addWorkflows(t, c.wf.RegistryN(0))
	addWorkflows(t, c.wf.RegistryN(1))
	appY := c.wf.DaprN(1).AppID()

	const (
		done    = api.InstanceID("crossapp-done")
		running = api.InstanceID("crossapp-running")
		force   = api.InstanceID("crossapp-force")
		forceR  = api.InstanceID("crossapp-force-recursive")
		started = api.InstanceID("crossapp-start")
	)
	mgmtX := c.wf.ManagementClientN(t, ctx, 0)
	mgmtY := c.wf.ManagementClientN(t, ctx, 1)

	workerY := c.wf.ConnectWorkerN(t, ctx, 1, c.wf.RegistryN(1))
	c.wf.WaitForConnectedWorkersN(t, ctx, 1, 1)
	_, err := mgmtY.ScheduleNewWorkflow(ctx, wfDone, api.WithInstanceID(done))
	require.NoError(t, err)
	waitStatus(t, ctx, mgmtY, done, statusCompleted)
	for _, id := range []api.InstanceID{running, force, forceR} {
		_, err = mgmtY.ScheduleNewWorkflow(ctx, wfEvent, api.WithInstanceID(id))
		require.NoError(t, err)
		waitStatus(t, ctx, mgmtY, id, statusRunning)
	}
	workerY.Disconnect(t)
	waitNoWorker(t, ctx, c.wf, 1, c.staged)

	c.wf.ConnectWorkerN(t, ctx, 0, c.wf.RegistryN(0))
	c.wf.WaitForConnectedWorkersN(t, ctx, 0, 1)

	assert.Equal(t, want(c.staged, noWorker, accepted), call(ctx, c.staged, func(ctx context.Context) error {
		_, err := mgmtX.FetchWorkflowMetadata(ctx, done, api.WithFetchAppID(appY))
		return err
	}), "get status")

	assert.Equal(t, want(c.staged, noWorker, accepted), call(ctx, c.staged, func(ctx context.Context) error {
		_, err := mgmtX.ScheduleNewWorkflow(ctx, wfDone, api.WithInstanceID(started), api.WithAppID(appY))
		return err
	}), "start")
	assert.Equal(t, want(c.staged, statusNotFound, statusPending), statusOf(ctx, mgmtY, started))

	assert.Equal(t, want(c.staged, noWorker, accepted), call(ctx, c.staged, func(ctx context.Context) error {
		return mgmtX.RaiseEvent(ctx, running, evGo, api.WithRaiseEventAppID(appY))
	}), "raise event")

	assert.Equal(t, want(c.staged, noWorker, accepted), call(ctx, c.staged, func(ctx context.Context) error {
		return mgmtX.PurgeWorkflowState(ctx, done, api.WithPurgeAppID(appY))
	}), "purge")
	assert.Equal(t, want(c.staged, statusCompleted, statusNotFound), statusOf(ctx, mgmtY, done))

	assert.Equal(t, unsupported, call(ctx, c.staged, func(ctx context.Context) error {
		return mgmtX.PurgeWorkflowState(ctx, force, api.WithPurgeAppID(appY), api.WithForcePurge(true))
	}), "force purge")
	assert.Equal(t, statusRunning, statusOf(ctx, mgmtY, force))

	assert.Equal(t, want(c.staged, noWorker, accepted), call(ctx, c.staged, func(ctx context.Context) error {
		return mgmtX.PurgeWorkflowState(ctx, forceR, api.WithPurgeAppID(appY), api.WithForcePurge(true), api.WithRecursivePurge(true))
	}), "recursive force purge")
	assert.Equal(t, want(c.staged, statusRunning, statusNotFound), statusOf(ctx, mgmtY, forceR))
}
