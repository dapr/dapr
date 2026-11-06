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

	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
)

func init() {
	suite.Register(new(wait))
}

// wait waits for a running workflow to start and to complete, and for a
// workflow that does not exist to start, while no worker is connected. It
// calls the sidecar directly: the durabletask client retries a failed wait
// until its deadline, which hides the error.
//   - off: all fail, no worker connected.
//   - on:  the wait for the running workflow to start returns at once; the
//     others block until a worker runs the workflow or the deadline passes.
type wait struct{ modes }

func (r *wait) Run(t *testing.T, ctx context.Context) {
	r.run(t, ctx, func(t *testing.T, m mode) {
		const id = "wait"
		m.startWith(t, ctx, 0, wfEvent, statusRunning, id)
		sidecar := protos.NewTaskHubSidecarServiceClient(m.wf.DaprN(m.idx(0)).GRPCConn(t, ctx))

		assert.Equal(t, want(m.staged, noWorker, accepted), m.call(ctx, func(ctx context.Context) error {
			_, err := sidecar.WaitForInstanceStart(ctx, &protos.GetInstanceRequest{InstanceId: id})
			return err
		}), "wait for start of the running workflow")
		assert.Equal(t, want(m.staged, noWorker, blocks), m.call(ctx, func(ctx context.Context) error {
			_, err := sidecar.WaitForInstanceCompletion(ctx, &protos.GetInstanceRequest{InstanceId: id})
			return err
		}), "wait for completion of the running workflow")
		assert.Equal(t, want(m.staged, noWorker, blocks), m.call(ctx, func(ctx context.Context) error {
			_, err := sidecar.WaitForInstanceStart(ctx, &protos.GetInstanceRequest{InstanceId: "wait-none"})
			return err
		}), "wait for start of a workflow that does not exist")
	})
}
