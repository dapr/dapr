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

package grpc

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(defaultmaxbodysize))
}

// defaultmaxbodysize starts daprd without --max-body-size and checks that the
// gRPC API server accepts a 10 MiB request and refuses a 17 MiB request.
type defaultmaxbodysize struct {
	daprd *procdaprd.Daprd
}

func (d *defaultmaxbodysize) Setup(t *testing.T) []framework.Option {
	d.daprd = procdaprd.New(t, procdaprd.WithInMemoryStateStore("mystore"))

	return []framework.Option{
		framework.WithProcesses(d.daprd),
	}
}

func (d *defaultmaxbodysize) Run(t *testing.T, ctx context.Context) {
	d.daprd.WaitUntilRunning(t, ctx)

	client := d.daprd.GRPCClient(t, ctx)

	value := bytes.Repeat([]byte("x"), 10<<20)
	_, err := client.SaveState(ctx, &rtv1.SaveStateRequest{
		StoreName: "mystore",
		States:    []*commonv1.StateItem{{Key: "large", Value: value}},
	})
	require.NoError(t, err)

	resp, err := client.GetState(ctx, &rtv1.GetStateRequest{StoreName: "mystore", Key: "large"})
	require.NoError(t, err)
	assert.Len(t, resp.GetData(), len(value))

	_, err = client.SaveState(ctx, &rtv1.SaveStateRequest{
		StoreName: "mystore",
		States:    []*commonv1.StateItem{{Key: "too-large", Value: bytes.Repeat([]byte("x"), 17<<20)}},
	})
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))
	require.ErrorContains(t, err, "received message larger than max")
	require.ErrorContains(t, err, "vs. 16777216)")
}
