/*
Copyright 2022 The Dapr Authors
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

package diagnostics

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opencensus.io/stats/view"
	"google.golang.org/grpc"
	grpcMetadata "google.golang.org/grpc/metadata"

	"github.com/dapr/dapr/pkg/api/grpc/metadata"
)

type fakeProxyStream struct {
	appID string
}

func (f *fakeProxyStream) Context() context.Context {
	if f.appID == "" {
		return context.Background()
	}

	ctx := context.Background()
	ctx = grpcMetadata.NewIncomingContext(ctx, grpcMetadata.New(map[string]string{GRPCProxyAppIDKey: f.appID}))
	ctx, _ = metadata.SetMetadataInTapHandle(ctx, nil)
	return ctx
}

func (f *fakeProxyStream) SetHeader(grpcMetadata.MD) error {
	return nil
}

func (f *fakeProxyStream) SendHeader(grpcMetadata.MD) error {
	return nil
}

func (f *fakeProxyStream) SetTrailer(grpcMetadata.MD) {
}

func (f *fakeProxyStream) SendMsg(m interface{}) error {
	return nil
}

func (f *fakeProxyStream) RecvMsg(m interface{}) error {
	return nil
}

func TestStreamingServerInterceptor(t *testing.T) {
	t.Run("not a proxy request, do not run pipeline", func(t *testing.T) {
		m := newGRPCMetrics()
		m.Init("test")

		i := m.StreamingServerInterceptor()
		s := &fakeProxyStream{}
		f := func(srv interface{}, stream grpc.ServerStream) error {
			return nil
		}

		err := i(nil, s, &grpc.StreamServerInfo{}, f)
		require.NoError(t, err)

		rows, err := view.RetrieveData("grpc.io/server/completed_rpcs")
		require.NoError(t, err)
		assert.Empty(t, rows)

		rowsLatency, err := view.RetrieveData("grpc.io/server/server_latency")
		require.NoError(t, err)
		assert.Empty(t, rowsLatency)
	})

	t.Run("proxy request, run pipeline", func(t *testing.T) {
		m := newGRPCMetrics()
		m.Init("test")

		i := m.StreamingServerInterceptor()
		s := &fakeProxyStream{
			appID: "test",
		}
		f := func(srv interface{}, stream grpc.ServerStream) error {
			return nil
		}

		err := i(nil, s, &grpc.StreamServerInfo{FullMethod: "/appv1.Test"}, f)
		require.NoError(t, err)

		rows, err := view.RetrieveData("grpc.io/server/completed_rpcs")
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, "app_id", rows[0].Tags[0].Key.Name())
		assert.Equal(t, "grpc_server_method", rows[0].Tags[1].Key.Name())
		assert.Equal(t, "grpc_server_status", rows[0].Tags[2].Key.Name())

		rows, err = view.RetrieveData("grpc.io/server/server_latency")
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, "app_id", rows[0].Tags[0].Key.Name())
		assert.Equal(t, "grpc_server_method", rows[0].Tags[1].Key.Name())
		assert.Equal(t, "grpc_server_status", rows[0].Tags[2].Key.Name())
	})
}

func TestStreamingClientInterceptor(t *testing.T) {
	t.Run("not a proxy request, do not run pipeline", func(t *testing.T) {
		m := newGRPCMetrics()
		m.Init("test")

		i := m.StreamingClientInterceptor()
		s := &fakeProxyStream{}
		f := func(srv interface{}, stream grpc.ServerStream) error {
			return nil
		}

		err := i(nil, s, &grpc.StreamServerInfo{}, f)
		require.NoError(t, err)

		rows, err := view.RetrieveData("grpc.io/client/completed_rpcs")
		require.NoError(t, err)
		assert.Empty(t, rows)

		rowsLatency, err := view.RetrieveData("grpc.io/client/roundtrip_latency")
		require.NoError(t, err)
		assert.Empty(t, rowsLatency)
	})

	t.Run("proxy request, run pipeline", func(t *testing.T) {
		m := newGRPCMetrics()
		m.Init("test")

		i := m.StreamingClientInterceptor()
		s := &fakeProxyStream{
			appID: "test",
		}
		f := func(srv interface{}, stream grpc.ServerStream) error {
			return nil
		}

		err := i(nil, s, &grpc.StreamServerInfo{FullMethod: "/appv1.Test"}, f)
		require.NoError(t, err)

		rows, err := view.RetrieveData("grpc.io/client/completed_rpcs")
		require.NoError(t, err)
		assert.Len(t, rows, 1)
		assert.Equal(t, "app_id", rows[0].Tags[0].Key.Name())
		assert.Equal(t, "grpc_client_method", rows[0].Tags[1].Key.Name())
		assert.Equal(t, "grpc_client_status", rows[0].Tags[2].Key.Name())

		rowsLatency, err := view.RetrieveData("grpc.io/client/roundtrip_latency")
		require.NoError(t, err)
		assert.Len(t, rowsLatency, 1)
		assert.Equal(t, "app_id", rows[0].Tags[0].Key.Name())
		assert.Equal(t, "grpc_client_method", rows[0].Tags[1].Key.Name())
		assert.Equal(t, "grpc_client_status", rows[0].Tags[2].Key.Name())
	})
}
