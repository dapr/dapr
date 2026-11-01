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

package grpcserver

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(headersize))
}

// headersize tests that the gRPC servers cap request headers at
// --read-buffer-size bytes, and at no less than 64 KiB.
type headersize struct {
	daprd      *daprd.Daprd
	daprdLarge *daprd.Daprd
}

func (h *headersize) Setup(t *testing.T) []framework.Option {
	h.daprd = daprd.New(t)
	h.daprdLarge = daprd.New(t, daprd.WithReadBufferSize("128Ki"))

	return []framework.Option{
		framework.WithProcesses(h.daprd, h.daprdLarge),
	}
}

func (h *headersize) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)
	h.daprdLarge.WaitUntilRunning(t, ctx)

	withMD := func(size int) context.Context {
		return metadata.AppendToOutgoingContext(ctx, "x-test", strings.Repeat("a", size))
	}

	t.Run("32KiB of metadata is accepted", func(t *testing.T) {
		_, err := h.daprd.GRPCClient(t, ctx).GetMetadata(withMD(32<<10), new(rtv1.GetMetadataRequest))
		require.NoError(t, err)
	})

	t.Run("128KiB of metadata is rejected by the API server", func(t *testing.T) {
		_, err := h.daprd.GRPCClient(t, ctx).GetMetadata(withMD(128<<10), new(rtv1.GetMetadataRequest))
		require.Equal(t, codes.Internal, status.Code(err), err)
		require.ErrorContains(t, err, "header list size to send violates the maximum size (65536 bytes)")
	})

	t.Run("128KiB of metadata is rejected by the internal server", func(t *testing.T) {
		client := h.daprd.InternalGRPCClient(t, ctx, nil, "", "")
		_, err := client.CallLocal(withMD(128<<10), new(internalv1pb.InternalInvokeRequest))
		require.Equal(t, codes.Internal, status.Code(err), err)
		require.ErrorContains(t, err, "header list size to send violates the maximum size (65536 bytes)")
	})

	t.Run("raised read-buffer-size is honoured", func(t *testing.T) {
		client := h.daprdLarge.GRPCClient(t, ctx)

		_, err := client.GetMetadata(withMD(127<<10), new(rtv1.GetMetadataRequest))
		require.NoError(t, err)

		_, err = client.GetMetadata(withMD(128<<10), new(rtv1.GetMetadataRequest))
		require.Equal(t, codes.Internal, status.Code(err), err)
		require.ErrorContains(t, err, "header list size to send violates the maximum size (131072 bytes)")
	})
}
