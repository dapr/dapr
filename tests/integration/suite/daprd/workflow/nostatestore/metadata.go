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
	suite.Register(new(metadata))
}

// metadata asserts that fetching workflow instance metadata on a sidecar
// without an actor state store returns an error instead of crashing the
// sidecar with a nil pointer dereference in LoadWorkflowState (via
// loadInternalState).
type metadata struct {
	daprd *daprd.Daprd
}

func (m *metadata) Setup(t *testing.T) []framework.Option {
	sched := scheduler.New(t)
	place := placement.New(t)

	m.daprd = daprd.New(t,
		daprd.WithScheduler(sched),
		daprd.WithPlacementAddresses(place.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(sched, place, m.daprd),
	}
}

func (m *metadata) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	cl := client.NewTaskHubGrpcClient(m.daprd.GRPCConn(t, ctx), logger.New(t))

	_, err := cl.FetchWorkflowMetadata(ctx, api.InstanceID("no-such-instance"))
	require.Error(t, err)
	s, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, s.Code())
	assert.Contains(t, err.Error(), "the state store is not configured to use the actor runtime")

	// The sidecar must survive the query.
	_, err = cl.FetchWorkflowMetadata(ctx, api.InstanceID("no-such-instance"))
	require.Error(t, err)
	s, ok = status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, s.Code())
}
