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
	"google.golang.org/grpc/codes"
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
	suite.Register(new(history))
}

// history asserts that querying workflow instance history on a sidecar
// without an actor state store returns an error instead of crashing the
// sidecar with a nil pointer dereference in LoadWorkflowState.
type history struct {
	daprd *daprd.Daprd
}

func (h *history) Setup(t *testing.T) []framework.Option {
	sched := scheduler.New(t)
	place := placement.New(t)

	h.daprd = daprd.New(t,
		daprd.WithScheduler(sched),
		daprd.WithPlacementAddresses(place.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(sched, place, h.daprd),
	}
}

func (h *history) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)

	cl := client.NewTaskHubGrpcClient(h.daprd.GRPCConn(t, ctx), logger.New(t))

	_, err := cl.GetInstanceHistory(ctx, api.InstanceID("no-such-instance"))
	require.Error(t, err)
	s, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, s.Code())
	assert.Contains(t, err.Error(), "the state store is not configured to use the actor runtime")

	// The sidecar must survive the query: a second call over the same
	// connection must fail with the same clean error rather than a transport
	// failure from a crashed daprd.
	_, err = cl.GetInstanceHistory(ctx, api.InstanceID("no-such-instance"))
	require.Error(t, err)
	s, ok = status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, s.Code())
}
