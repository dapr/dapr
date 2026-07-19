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

package nostatestore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
)

func init() {
	suite.Register(new(purge))
}

// purge asserts that purging a workflow on a sidecar without an actor state
// store returns an error instead of crashing the sidecar, for the plain,
// forced, and recursive purge variants. Forced and recursive purge load the
// actor state directly (purgeWorkflowForce / loadInternalState) and surface
// the actor-runtime error; plain purge dispatches to the workflow actor,
// which is never located without an actor state store. Neither must crash
// the sidecar.
type purge struct {
	daprd *daprd.Daprd
}

func (p *purge) Setup(t *testing.T) []framework.Option {
	sched := scheduler.New(t)
	place := placement.New(t)

	p.daprd = daprd.New(t,
		daprd.WithScheduler(sched),
		daprd.WithPlacementAddresses(place.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(sched, place, p.daprd),
	}
}

func (p *purge) Run(t *testing.T, ctx context.Context) {
	p.daprd.WaitUntilRunning(t, ctx)

	cl := client.NewTaskHubGrpcClient(p.daprd.GRPCConn(t, ctx), logger.New(t))

	for _, tc := range []struct {
		name string
		opts []api.PurgeOptions
		// actorRuntimeErr is true when the variant loads the actor state
		// directly and therefore surfaces the actor-runtime error; plain
		// purge instead fails to locate the (unregistered) workflow actor.
		actorRuntimeErr bool
	}{
		{name: "plain"},
		{name: "forced", opts: []api.PurgeOptions{api.WithForcePurge(true)}, actorRuntimeErr: true},
		{name: "recursive", opts: []api.PurgeOptions{api.WithRecursivePurge(true)}, actorRuntimeErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := cl.PurgeWorkflowState(ctx, api.InstanceID("no-such-instance"), tc.opts...)
			require.Error(t, err)
			// A gRPC status (rather than a transport-level failure) proves the
			// sidecar answered the request instead of crashing.
			_, ok := status.FromError(err)
			require.True(t, ok)
			if tc.actorRuntimeErr {
				assert.Contains(t, err.Error(), "the state store is not configured to use the actor runtime")
			}
		})
	}

	// The sidecar must survive all of the above.
	err := cl.PurgeWorkflowState(ctx, api.InstanceID("no-such-instance"))
	require.Error(t, err)
}
