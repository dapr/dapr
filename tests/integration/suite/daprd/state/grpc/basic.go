/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	daprd *procdaprd.Daprd
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.daprd = procdaprd.New(t, procdaprd.WithInMemoryActorStateStore("mystore"))

	return []framework.Option{
		framework.WithProcesses(b.daprd),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.DialContext(ctx, b.daprd.GRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	t.Run("bad request", func(t *testing.T) {
		for _, req := range []*rtv1.SaveStateRequest{
			nil,
			{},
			{StoreName: "mystore", States: []*commonv1.StateItem{{}}},
			{StoreName: "mystore", States: []*commonv1.StateItem{{Value: []byte("value1")}}},
		} {
			_, err = client.SaveState(ctx, req)
			require.Error(t, err)
		}
	})

	t.Run("good request", func(t *testing.T) {
		for _, req := range []*rtv1.SaveStateRequest{
			{StoreName: "mystore"},
			{StoreName: "mystore", States: []*commonv1.StateItem{}},
			{StoreName: "mystore", States: []*commonv1.StateItem{{Key: "key1"}}},
			{StoreName: "mystore", States: []*commonv1.StateItem{{Key: "key1", Value: []byte("value1")}}},
			{StoreName: "mystore", States: []*commonv1.StateItem{{Key: "key1", Value: []byte("value1")}, {Key: "key2", Value: []byte("value2")}}},
			{StoreName: "mystore", States: []*commonv1.StateItem{
				{Key: "key1", Value: []byte("value1")},
				{Key: "key2", Value: []byte("value2")},
				{Key: "key1", Value: []byte("value1")},
				{Key: "key2", Value: []byte("value2")},
			}},
		} {
			_, err = client.SaveState(ctx, req)
			require.NoError(t, err)
		}
	})
}
