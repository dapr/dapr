/*
Copyright 2023 The Dapr Authors
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

package authz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	grpcinsecure "google.golang.org/grpc/credentials/insecure"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(nomtls))
}

// nomtls tests placement can find quorum with tls disabled.
type nomtls struct {
	place *placement.Placement
}

func (n *nomtls) Setup(t *testing.T) []framework.Option {
	n.place = placement.New(t)

	return []framework.Option{
		framework.WithProcesses(n.place),
	}
}

func (n *nomtls) Run(t *testing.T, ctx context.Context) {
	n.place.WaitUntilRunning(t, ctx)

	require.Eventually(t, func() bool {
		return n.place.IsLeader(t, ctx)
	}, time.Second*10, time.Millisecond*10)

	// Can create hosts with any appIDs or namespaces.
	_, err := n.establishStream(t, ctx, &v1pb.Host{
		Id:        "foo",
		Namespace: "bar",
	})
	require.NoError(t, err)

	_, err = n.establishStream(t, ctx, &v1pb.Host{
		Id:        "bar",
		Namespace: "foo",
	})
	require.NoError(t, err)

	_, err = n.establishStream(t, ctx, &v1pb.Host{
		Id:        "bar",
		Namespace: "ns1",
	})
	require.NoError(t, err)
}

func (n *nomtls) establishStream(t *testing.T, ctx context.Context, firstMessage *v1pb.Host) (v1pb.Placement_ReportDaprStatusClient, error) {
	t.Helper()

	host := n.place.Address()
	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, host, grpc.WithBlock(), grpc.WithReturnConnectionError(),
		grpc.WithTransportCredentials(grpcinsecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := v1pb.NewPlacementClient(conn)

	stream, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)

	err = stream.Send(firstMessage)
	if err != nil {
		return nil, err
	}
	_, err = stream.Recv()

	return stream, err
}
