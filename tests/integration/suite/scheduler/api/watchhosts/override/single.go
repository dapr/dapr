/*
Copyright 2025 The Dapr Authors
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

package override

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(single))
}

type single struct {
	scheduler *scheduler.Scheduler
}

func (s *single) Setup(t *testing.T) []framework.Option {
	s.scheduler = scheduler.New(t,
		scheduler.WithOverrideBroadcastHostPort("1.2.3.4:5678"),
	)

	return []framework.Option{
		framework.WithProcesses(s.scheduler),
	}
}

func (s *single) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)

	stream, err := s.scheduler.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
	require.NoError(t, err)

	resp, err := stream.Recv()
	require.NoError(t, err)

	require.Len(t, resp.GetHosts(), 1)
	assert.Equal(t, "1.2.3.4:5678", resp.GetHosts()[0].GetAddress())
}
