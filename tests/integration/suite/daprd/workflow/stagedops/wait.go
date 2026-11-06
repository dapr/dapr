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
	"github.com/dapr/durabletask-go/api/protos"
)

func init() {
	suite.Register(new(waitoff))
	suite.Register(new(waiton))
}

type (
	waitoff struct{ wait }
	waiton  struct{ wait }
)

func (w *waiton) Setup(t *testing.T) []framework.Option {
	w.staged = true
	return w.wait.Setup(t)
}

// wait waits for a running workflow to start and to complete, and for a
// workflow that does not exist to start, while no worker is connected. It
// calls the sidecar directly: the durabletask client retries a failed wait
// until its deadline, which hides the error.
//   - off: all fail, no worker connected.
//   - on:  the wait for the running workflow to start returns at once; the
//     others block until a worker runs the workflow or the deadline passes.
type wait struct{ mode }

func (r *wait) Run(t *testing.T, ctx context.Context) {
	r.wf.WaitUntilRunning(t, ctx)
	addWorkflows(t, r.wf.Registry())

	const id = "wait"
	startWith(t, ctx, r.wf, 0, r.staged, wfEvent, statusRunning, api.InstanceID(id))
	sidecar := protos.NewTaskHubSidecarServiceClient(r.wf.Dapr().GRPCConn(t, ctx))

	assert.Equal(t, want(r.staged, noWorker, accepted), call(ctx, r.staged, func(ctx context.Context) error {
		_, err := sidecar.WaitForInstanceStart(ctx, &protos.GetInstanceRequest{InstanceId: id})
		return err
	}), "wait for start of the running workflow")
	assert.Equal(t, want(r.staged, noWorker, blocks), call(ctx, r.staged, func(ctx context.Context) error {
		_, err := sidecar.WaitForInstanceCompletion(ctx, &protos.GetInstanceRequest{InstanceId: id})
		return err
	}), "wait for completion of the running workflow")
	assert.Equal(t, want(r.staged, noWorker, blocks), call(ctx, r.staged, func(ctx context.Context) error {
		_, err := sidecar.WaitForInstanceStart(ctx, &protos.GetInstanceRequest{InstanceId: "wait-none"})
		return err
	}), "wait for start of a workflow that does not exist")
}
