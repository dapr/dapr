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

package workflow

import (
	"context"
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
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

// Cluster is an mTLS control plane and a SQLite actor state store for
// workflow tests that set their daprds' features themselves, rather than
// through the environment-driven matrix of the process/workflow harness.
type Cluster struct {
	Sentry    *sentry.Sentry
	DB        *sqlite.SQLite
	Placement *placement.Placement
	Scheduler *scheduler.Scheduler
}

func NewCluster(t *testing.T) *Cluster {
	t.Helper()
	c := &Cluster{Sentry: sentry.New(t)}
	c.DB = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())
	c.Placement = placement.New(t, placement.WithSentry(t, c.Sentry))
	c.Scheduler = scheduler.New(t, scheduler.WithSentry(c.Sentry), scheduler.WithID("dapr-scheduler-server-0"))
	return c
}

// Daprd returns a daprd on the cluster, with the actor state store
// component.
func (c *Cluster) Daprd(t *testing.T, opts ...daprd.Option) *daprd.Daprd {
	t.Helper()
	return daprd.New(t, append([]daprd.Option{
		daprd.WithSentry(t, c.Sentry),
		daprd.WithPlacementAddresses(c.Placement.Address()),
		daprd.WithScheduler(c.Scheduler),
		daprd.WithResourceFiles(c.DB.GetComponent(t)),
	}, opts...)...)
}

// Options returns the framework options running the cluster and daprds.
func (c *Cluster) Options(daprds ...*daprd.Daprd) []framework.Option {
	opts := make([]framework.Option, 0, 1+len(daprds))
	opts = append(opts, framework.WithProcesses(c.Sentry, c.DB, c.Placement, c.Scheduler))
	for _, d := range daprds {
		opts = append(opts, framework.WithProcesses(d))
	}
	return opts
}

// Replace stops old and starts a daprd with the same app ID and opts.
func (c *Cluster) Replace(t *testing.T, ctx context.Context, old *daprd.Daprd, opts ...daprd.Option) *daprd.Daprd {
	t.Helper()
	old.Kill(t)
	d := c.Daprd(t, append(opts, daprd.WithAppID(old.AppID()))...)
	d.Run(t, ctx)
	t.Cleanup(func() { d.Cleanup(t) })
	return d
}

// StartWorker waits for d and starts a workflow worker for reg on it.
func StartWorker(t *testing.T, ctx context.Context, d *daprd.Daprd, reg *dworkflow.Registry) *dworkflow.Client {
	t.Helper()
	d.WaitUntilRunning(t, ctx)
	cl := dworkflow.NewClientWithLogger(d.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, cl.StartWorker(ctx, reg))
	return cl
}

// WaitForEventRegistry registers workflow name, which waits for the event
// and returns its string input followed by the event's string payload.
func WaitForEventRegistry(t *testing.T, name, event string) *dworkflow.Registry {
	t.Helper()
	reg := dworkflow.NewRegistry()
	require.NoError(t, reg.AddWorkflowN(name, func(ctx *dworkflow.WorkflowContext) (any, error) {
		var in, ev string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		if err := ctx.WaitForExternalEvent(event, time.Minute).Await(&ev); err != nil {
			return nil, err
		}
		return in + ev, nil
	}))
	return reg
}

// ScheduleUntilPersisted schedules workflow name with input and waits until
// it runs and its first turn is persisted.
func ScheduleUntilPersisted(t *testing.T, ctx context.Context, cl *dworkflow.Client, db *sqlite.SQLite, name string, input any) string {
	t.Helper()
	id, err := cl.ScheduleWorkflow(ctx, name, dworkflow.WithInput(input))
	require.NoError(t, err)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, err := cl.FetchWorkflowMetadata(ctx, id)
		if assert.NoError(c, err) {
			assert.Equal(c, dworkflow.StatusRunning, meta.RuntimeStatus)
		}
		assert.Contains(c, StateRows(t, ctx, db, id), "history-000001")
	}, 20*time.Second, 50*time.Millisecond)
	return id
}
