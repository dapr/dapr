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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(retentionoff))
	suite.Register(new(retentionon))
}

type (
	retentionoff struct{ retention }
	retentionon  struct{ retention }
)

func (r *retentionon) Setup(t *testing.T) []framework.Option {
	r.staged = true
	return r.retention.Setup(t)
}

// retentionPeriod is how long a completed workflow is kept.
const retentionPeriod = time.Second * 6

// retention lets the retention period of a completed workflow pass while no
// worker is connected.
//   - off: the workflow is not purged while no worker is connected; the
//     scheduler holds the retention reminder, and the workflow is purged
//     once a worker connects.
//   - on:  the same.
type retention struct{ mode }

func (r *retention) Setup(t *testing.T) []framework.Option {
	r.wf = newWorkflow(t, r.staged, 1,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfpolicy
spec:
  workflow:
    stateRetentionPolicy:
      completed: "`+retentionPeriod.String()+`"
`)),
	)
	return []framework.Option{framework.WithProcesses(r.wf)}
}

func (r *retention) Run(t *testing.T, ctx context.Context) {
	r.wf.WaitUntilRunning(t, ctx)
	addWorkflows(t, r.wf.Registry())

	const id = api.InstanceID("retention")
	startWith(t, ctx, r.wf, 0, r.staged, wfDone, statusCompleted, id)
	completed := time.Now()
	mgmt := r.wf.ManagementClient(t, ctx)

	// startWith returns once the worker is gone; that must be before the
	// retention reminder is due, or this checks nothing.
	require.Less(t, time.Since(completed), retentionPeriod/2)

	// Past the retention period, with a margin for the reminder to fire.
	time.Sleep(time.Until(completed.Add(retentionPeriod + time.Second*5)))
	assert.Equal(t, statusCompleted, statusOf(ctx, mgmt, id))

	r.wf.ConnectWorker(t, ctx, r.wf.Registry())
	r.wf.WaitForConnectedWorkers(t, ctx, 1)
	waitStatus(t, ctx, mgmt, id, statusNotFound)
}
