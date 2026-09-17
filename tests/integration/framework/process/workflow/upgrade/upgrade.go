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

package upgrade

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

// Upgrade is a control plane plus two daprds that share an app ID and an
// on-disk actor state store: From runs the binary at
// DAPR_INTEGRATION_DAPRD_LEGACY_PATH and To runs the one under test, or the
// reverse with WithMasterToLegacy. Run starts only the control plane; the
// test starts From, kills it mid-workflow and then starts To.
type Upgrade struct {
	place *placement.Placement
	sched *scheduler.Scheduler
	db    *sqlite.SQLite
	from  *daprd.Daprd
	to    *daprd.Daprd
}

// New skips the test when no legacy daprd binary is configured or a workflow
// mode other than the default is selected.
func New(t *testing.T, fopts ...Option) *Upgrade {
	t.Helper()

	legacy := binary.EnvValue("daprd_legacy")
	if legacy == "" {
		t.Skip("DAPR_INTEGRATION_DAPRD_LEGACY_PATH not set")
	}
	if workflow.ClusteredDeploymentFromEnv() || workflow.FastPathFromEnv() ||
		workflow.SigningFromEnv() || workflow.SchedulerPlacementFromEnv() {
		t.Skip("upgrade tests run in the default workflow mode only")
	}

	var opts options
	for _, fopt := range fopts {
		fopt(&opts)
	}

	u := &Upgrade{
		place: placement.New(t),
		sched: scheduler.New(t),
		db:    sqlite.New(t, sqlite.WithActorStateStore(true)),
	}

	appID := uuid.New().String()
	newDaprd := func(dopts ...daprd.Option) *daprd.Daprd {
		return daprd.New(t, append([]daprd.Option{
			daprd.WithAppID(appID),
			daprd.WithResourceFiles(u.db.GetComponent(t)),
			daprd.WithPlacementAddresses(u.place.Address()),
			daprd.WithSchedulerAddresses(u.sched.Address()),
		}, dopts...)...)
	}
	u.from = newDaprd(daprd.WithExecPath(legacy))
	u.to = newDaprd()
	if opts.masterToLegacy {
		u.from, u.to = u.to, u.from
	}

	return u
}

func (u *Upgrade) Run(t *testing.T, ctx context.Context) {
	u.place.Run(t, ctx)
	u.sched.Run(t, ctx)
	u.db.Run(t, ctx)
}

func (u *Upgrade) Cleanup(t *testing.T) {
	u.db.Cleanup(t)
	u.sched.Cleanup(t)
	u.place.Cleanup(t)
}

func (u *Upgrade) From() *daprd.Daprd {
	return u.from
}

func (u *Upgrade) To() *daprd.Daprd {
	return u.to
}

func (u *Upgrade) Scheduler() *scheduler.Scheduler {
	return u.sched
}

// Start runs d, connects a worker serving reg to it and returns the client
// once d has registered the workflow actor types and sees the worker.
func (u *Upgrade) Start(t *testing.T, ctx context.Context, d *daprd.Daprd, reg *task.TaskRegistry) *client.TaskHubGrpcClient {
	t.Helper()

	u.place.WaitUntilRunning(t, ctx)
	u.sched.WaitUntilRunning(t, ctx)
	d.Run(t, ctx)
	t.Cleanup(func() { d.Cleanup(t) })
	d.WaitUntilRunning(t, ctx)

	cl := client.NewTaskHubGrpcClient(d.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, cl.StartWorkItemListener(ctx, reg))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		md := d.GetMetadata(t, ctx)
		if !assert.NotNil(c, md) || !assert.NotNil(c, md.ActorRuntime) {
			return
		}
		assert.GreaterOrEqual(c, len(md.ActorRuntime.ActiveActors), 2)
		if md.Workflows != nil {
			assert.GreaterOrEqual(c, md.Workflows.ConnectedWorkers, 1)
		}
	}, time.Second*30, time.Millisecond*10)

	return cl
}
