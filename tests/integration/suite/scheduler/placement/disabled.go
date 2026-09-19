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

package placement

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(disabled))
}

// disabled asserts a scheduler without placement enabled advertises no
// placement capability and refuses report streams with Unimplemented,
// matching a scheduler version predating the RPC.
type disabled struct {
	sched *scheduler.Scheduler
}

func (d *disabled) Setup(t *testing.T) []framework.Option {
	d.sched = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(d.sched),
	}
}

func (d *disabled) Run(t *testing.T, ctx context.Context) {
	d.sched.WaitUntilRunning(t, ctx)

	stream, err := d.sched.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
	require.NoError(t, err)
	//nolint:errcheck
	defer stream.CloseSend()
	resp, err := stream.Recv()
	require.NoError(t, err)
	require.Len(t, resp.GetHosts(), 1)
	assert.False(t, resp.GetHosts()[0].GetSchedulerPlacementEnabled())
	assert.False(t, resp.GetHosts()[0].GetLeader())

	rstream, err := d.sched.Client(t, ctx).ReportActorTypes(ctx)
	require.NoError(t, err)
	_, err = rstream.Recv()
	require.Equal(t, codes.Unimplemented, status.Code(err))
}
