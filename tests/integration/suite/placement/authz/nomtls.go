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

	host := n.place.Address()
	conn, err := grpc.DialContext(ctx, host, grpc.WithBlock(), grpc.WithReturnConnectionError(),
		grpc.WithTransportCredentials(grpcinsecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	client := v1pb.NewPlacementClient(conn)

	// Can create hosts with any appIDs or namespaces.
	stream := establishStream(t, ctx, client)
	require.NoError(t, stream.Send(new(v1pb.Host)))
	waitForUnlock(t, stream)
	_, err = stream.Recv()
	require.NoError(t, err)

	stream = establishStream(t, ctx, client)
	require.NoError(t, stream.Send(&v1pb.Host{Name: "bar"}))
	waitForUnlock(t, stream)
	_, err = stream.Recv()
	require.NoError(t, err)
}
