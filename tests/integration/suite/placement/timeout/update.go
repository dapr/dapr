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

package timeout

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(update))
}

type update struct {
	place *placement.Placement
}

func (u *update) Setup(t *testing.T) []framework.Option {
	u.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second),
	)

	return []framework.Option{
		framework.WithProcesses(u.place),
	}
}

func (u *update) Run(t *testing.T, ctx context.Context) {
	u.place.WaitUntilRunning(t, ctx)

	assert.Eventually(t, func() bool {
		return u.place.HasLeader(t, ctx)
	}, time.Second*10, time.Millisecond*10)

	client := u.place.Client(t, ctx)

	stream, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&v1pb.Host{
		Name:      "myapp",
		Port:      1234,
		Entities:  []string{"someactor"},
		Id:        "myapp",
		Namespace: "default",
	}))
	resp, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", resp.GetOperation())

	require.NoError(t, stream.Send(&v1pb.Host{
		Name:      "myapp",
		Port:      1234,
		Entities:  []string{"someactor"},
		Id:        "myapp",
		Namespace: "default",
	}))
	resp, err = stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "update", resp.GetOperation())

	errCh := make(chan error, 1)
	go func() {
		_, serr := stream.Recv()
		errCh <- serr
	}()

	select {
	case <-time.After(5 * time.Second):
		require.Fail(t, "expected timeout did not occur")
	case serr := <-errCh:
		require.Error(t, serr)
		s, ok := status.FromError(serr)
		require.True(t, ok)
		assert.Equal(t, codes.DeadlineExceeded, s.Code())
		assert.Equal(t, "dissemination timeout for version 1", s.Message())
	}
}
