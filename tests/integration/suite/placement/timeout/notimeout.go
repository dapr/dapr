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

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(notimeout))
}

type notimeout struct {
	place *placement.Placement
}

func (n *notimeout) Setup(t *testing.T) []framework.Option {
	n.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second),
	)

	return []framework.Option{
		framework.WithProcesses(n.place),
	}
}

func (n *notimeout) Run(t *testing.T, ctx context.Context) {
	n.place.WaitUntilRunning(t, ctx)

	assert.Eventually(t, func() bool {
		return n.place.HasLeader(t, ctx)
	}, time.Second*10, time.Millisecond*10)

	client := n.place.Client(t, ctx)

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

	require.NoError(t, stream.Send(&v1pb.Host{
		Name:      "myapp",
		Port:      1234,
		Entities:  []string{"someactor"},
		Id:        "myapp",
		Namespace: "default",
	}))
	resp, err = stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "unlock", resp.GetOperation())

	require.NoError(t, stream.Send(&v1pb.Host{
		Name:      "myapp",
		Port:      1234,
		Entities:  []string{"someactor"},
		Id:        "myapp",
		Namespace: "default",
	}))

	errCh := make(chan error, 1)
	go func() {
		_, serr := stream.Recv()
		errCh <- serr
	}()

	select {
	case <-time.After(3 * time.Second):
	case serr := <-errCh:
		require.FailNow(t, "expected no timeout error but got one", "error: %v", serr)
	}
}
