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
type crossapp struct{ modes }

func (c *crossapp) Setup(t *testing.T) []framework.Option {
	return c.setup(t, 2, nil)
}

func (c *crossapp) Run(t *testing.T, ctx context.Context) {
	c.run(t, ctx, func(t *testing.T, m mode) {
		const x, y = 0, 1
		appY := m.wf.DaprN(m.idx(y)).AppID()

		const (
			done    = api.InstanceID("crossapp-done")
			running = api.InstanceID("crossapp-running")
			force   = api.InstanceID("crossapp-force")
			forceR  = api.InstanceID("crossapp-force-recursive")
			started = api.InstanceID("crossapp-start")
		)
		mgmtX := m.mgmt(t, ctx, x)
		mgmtY := m.mgmt(t, ctx, y)

		workerY := m.connect(t, ctx, y)
		_, err := mgmtY.ScheduleNewWorkflow(ctx, wfDone, api.WithInstanceID(done))
		require.NoError(t, err)
		waitStatus(t, ctx, mgmtY, done, statusCompleted)
		for _, id := range []api.InstanceID{running, force, forceR} {
			_, err = mgmtY.ScheduleNewWorkflow(ctx, wfEvent, api.WithInstanceID(id))
			require.NoError(t, err)
			waitStatus(t, ctx, mgmtY, id, statusRunning)
		}
		workerY.Disconnect(t)
		m.waitNoWorker(t, ctx, y)

		m.connect(t, ctx, x)

		assert.Equal(t, want(m.staged, noWorker, accepted), m.call(ctx, func(ctx context.Context) error {
			_, err := mgmtX.FetchWorkflowMetadata(ctx, done, api.WithFetchAppID(appY))
			return err
		}), "get status")

		assert.Equal(t, want(m.staged, noWorker, accepted), m.call(ctx, func(ctx context.Context) error {
			_, err := mgmtX.ScheduleNewWorkflow(ctx, wfDone, api.WithInstanceID(started), api.WithAppID(appY))
			return err
		}), "start")
		assert.Equal(t, want(m.staged, statusNotFound, statusPending), statusOf(ctx, mgmtY, started))

		assert.Equal(t, want(m.staged, noWorker, accepted), m.call(ctx, func(ctx context.Context) error {
			return mgmtX.RaiseEvent(ctx, running, evGo, api.WithRaiseEventAppID(appY))
		}), "raise event")

		assert.Equal(t, want(m.staged, noWorker, accepted), m.call(ctx, func(ctx context.Context) error {
			return mgmtX.PurgeWorkflowState(ctx, done, api.WithPurgeAppID(appY))
		}), "purge")
		assert.Equal(t, want(m.staged, statusCompleted, statusNotFound), statusOf(ctx, mgmtY, done))

		assert.Equal(t, unsupported, m.call(ctx, func(ctx context.Context) error {
			return mgmtX.PurgeWorkflowState(ctx, force, api.WithPurgeAppID(appY), api.WithForcePurge(true))
		}), "force purge")
		assert.Equal(t, statusRunning, statusOf(ctx, mgmtY, force))

		assert.Equal(t, want(m.staged, noWorker, accepted), m.call(ctx, func(ctx context.Context) error {
			return mgmtX.PurgeWorkflowState(ctx, forceR, api.WithPurgeAppID(appY), api.WithForcePurge(true), api.WithRecursivePurge(true))
		}), "recursive force purge")
		assert.Equal(t, want(m.staged, statusRunning, statusNotFound), statusOf(ctx, mgmtY, forceR))
	})
}
