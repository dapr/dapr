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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(terminateoff))
	suite.Register(new(terminateon))
}

type (
	terminateoff struct{ terminate }
	terminateon  struct{ terminate }
)

func (r *terminateon) Setup(t *testing.T) []framework.Option {
	r.staged = true
	return r.terminate.Setup(t)
}

// terminate terminates a running workflow while no worker is connected.
//   - off: fails, no worker connected.
//   - on:  accepted, but the instance stays RUNNING until a worker connects;
//     only then is it TERMINATED. A retention policy for terminated
//     workflows therefore starts counting from the worker's connect.
type terminate struct{ mode }

func (r *terminate) Run(t *testing.T, ctx context.Context) {
	r.wf.WaitUntilRunning(t, ctx)
	addWorkflows(t, r.wf.Registry())

	const id = api.InstanceID("terminate")
	startWith(t, ctx, r.wf, 0, r.staged, wfEvent, statusRunning, id)
	mgmt := r.wf.ManagementClient(t, ctx)

	got := call(ctx, r.staged, func(ctx context.Context) error {
		return mgmt.TerminateWorkflow(ctx, id)
	})
	assert.Equal(t, want(r.staged, noWorker, accepted), got)
	assert.Equal(t, statusRunning, statusOf(ctx, mgmt, id))

	if !r.staged {
		return
	}

	r.wf.ConnectWorker(t, ctx, r.wf.Registry())
	r.wf.WaitForConnectedWorkers(t, ctx, 1)
	waitStatus(t, ctx, mgmt, id, statusTerminated)
}
