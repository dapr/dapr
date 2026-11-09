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
	"github.com/dapr/dapr/tests/integration/framework/process/workflow/stagedops"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(crossapp))
}

// crossapp calls, from app X which has a worker, each operation on workflows
// of app Y which has none. Each mode has its own X and Y.
//   - off: get status, start, raise, purge and recursive force purge all
//     fail, no worker connected.
//   - on:  all are accepted.
//
// A force purge without recursive fails in both modes: a cross-app force
// purge must be recursive.
type crossapp struct{ stagedops.Modes }

func (c *crossapp) Setup(t *testing.T) []framework.Option {
	return c.SetupN(t, 2, nil)
}

func (c *crossapp) Run(t *testing.T, ctx context.Context) {
	c.RunModes(t, ctx, func(t *testing.T, m stagedops.Mode) {
		const x, y = 0, 1
		appY := m.WF.DaprN(m.Idx(y)).AppID()

		const (
			done    = api.InstanceID("crossapp-done")
			running = api.InstanceID("crossapp-running")
			force   = api.InstanceID("crossapp-force")
			forceR  = api.InstanceID("crossapp-force-recursive")
			started = api.InstanceID("crossapp-start")
		)
		mgmtX := m.Mgmt(t, ctx, x)
		mgmtY := m.Mgmt(t, ctx, y)

		workerY := m.Connect(t, ctx, y)
		_, err := mgmtY.ScheduleNewWorkflow(ctx, stagedops.WfDone, api.WithInstanceID(done))
		require.NoError(t, err)
		stagedops.WaitStatus(t, ctx, mgmtY, done, stagedops.StatusCompleted)
		for _, id := range []api.InstanceID{running, force, forceR} {
			_, err = mgmtY.ScheduleNewWorkflow(ctx, stagedops.WfEvent, api.WithInstanceID(id))
			require.NoError(t, err)
			stagedops.WaitStatus(t, ctx, mgmtY, id, stagedops.StatusRunning)
		}
		workerY.Disconnect(t)
		m.WaitNoWorker(t, ctx, y)

		m.Connect(t, ctx, x)

		assert.Equal(t, stagedops.Want(m.Staged, stagedops.NoWorker, stagedops.Accepted), m.Call(ctx, func(ctx context.Context) error {
			_, err := mgmtX.FetchWorkflowMetadata(ctx, done, api.WithFetchAppID(appY))
			return err
		}), "get status")

		assert.Equal(t, stagedops.Want(m.Staged, stagedops.NoWorker, stagedops.Accepted), m.Call(ctx, func(ctx context.Context) error {
			_, err := mgmtX.ScheduleNewWorkflow(ctx, stagedops.WfDone, api.WithInstanceID(started), api.WithAppID(appY))
			return err
		}), "start")
		assert.Equal(t, stagedops.Want(m.Staged, stagedops.StatusNotFound, stagedops.StatusPending), stagedops.StatusOf(ctx, mgmtY, started))

		assert.Equal(t, stagedops.Want(m.Staged, stagedops.NoWorker, stagedops.Accepted), m.Call(ctx, func(ctx context.Context) error {
			return mgmtX.RaiseEvent(ctx, running, stagedops.EvGo, api.WithRaiseEventAppID(appY))
		}), "raise event")

		assert.Equal(t, stagedops.Want(m.Staged, stagedops.NoWorker, stagedops.Accepted), m.Call(ctx, func(ctx context.Context) error {
			return mgmtX.PurgeWorkflowState(ctx, done, api.WithPurgeAppID(appY))
		}), "purge")
		assert.Equal(t, stagedops.Want(m.Staged, stagedops.StatusCompleted, stagedops.StatusNotFound), stagedops.StatusOf(ctx, mgmtY, done))

		assert.Equal(t, stagedops.Unsupported, m.Call(ctx, func(ctx context.Context) error {
			return mgmtX.PurgeWorkflowState(ctx, force, api.WithPurgeAppID(appY), api.WithForcePurge(true))
		}), "force purge")
		assert.Equal(t, stagedops.StatusRunning, stagedops.StatusOf(ctx, mgmtY, force))

		assert.Equal(t, stagedops.Want(m.Staged, stagedops.NoWorker, stagedops.Accepted), m.Call(ctx, func(ctx context.Context) error {
			return mgmtX.PurgeWorkflowState(ctx, forceR, api.WithPurgeAppID(appY), api.WithForcePurge(true), api.WithRecursivePurge(true))
		}), "recursive force purge")
		assert.Equal(t, stagedops.Want(m.Staged, stagedops.StatusRunning, stagedops.StatusNotFound), stagedops.StatusOf(ctx, mgmtY, forceR))
	})
}
