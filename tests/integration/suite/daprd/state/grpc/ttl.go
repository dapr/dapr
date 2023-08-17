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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
	suite.Register(new(ttl))
}

type ttl struct {
	daprd *procdaprd.Daprd
}

func (l *ttl) Setup(t *testing.T) []framework.Option {
	l.daprd = procdaprd.New(t, procdaprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.in-memory
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(l.daprd),
	}
}

func (l *ttl) Run(t *testing.T, ctx context.Context) {
	l.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.DialContext(ctx, fmt.Sprintf("localhost:%d", l.daprd.GRPCPort()), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	now := time.Now()
	_, err = client.SaveState(ctx, &rtv1.SaveStateRequest{
		StoreName: "mystore",
		States: []*commonv1.StateItem{
			{Key: "key1", Value: []byte("value1"), Metadata: map[string]string{"ttlInSeconds": "3"}},
		},
	})
	require.NoError(t, err)

	resp, err := client.GetState(ctx, &rtv1.GetStateRequest{
		StoreName: "mystore", Key: "key1",
	})
	require.NoError(t, err)
	assert.Equal(t, "value1", string(resp.Data))
	ttlExpireTime, err := time.Parse(time.RFC3339, resp.GetMetadata()["ttlExpireTime"])
	require.NoError(t, err)
	assert.InDelta(t, now.Add(3*time.Second).Unix(), ttlExpireTime.Unix(), 1)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := client.GetState(ctx, &rtv1.GetStateRequest{
			StoreName: "mystore", Key: "key1",
		})
		require.NoError(c, err)
		assert.Empty(c, resp.Data)
	}, 5*time.Second, 100*time.Millisecond)
}
