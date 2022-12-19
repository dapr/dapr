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

package pluggable

import (
	"context"
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	proto "github.com/dapr/dapr/pkg/proto/components/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
)

type fakeClient struct {
	pingCalled atomic.Int64
}

func (f *fakeClient) Ping(context.Context, *proto.PingRequest, ...grpc.CallOption) (*proto.PingResponse, error) {
	f.pingCalled.Add(1)
	return &proto.PingResponse{}, nil
}

func TestGRPCConnector(t *testing.T) {
	// gRPC Pluggable component requires Unix Domain Socket to work, I'm skipping this test when running on windows.
	if runtime.GOOS == "windows" {
		return
	}

	t.Run("grpc connection should be idle or ready when the process is listening to the socket due to withblock usage", func(t *testing.T) {
		fakeFactoryCalled := 0
		clientFake := &fakeClient{}
		fakeFactory := func(grpc.ClientConnInterface) *fakeClient {
			fakeFactoryCalled++
			return clientFake
		}
		connector := NewGRPCConnector("/tmp/socket.sock", fakeFactory)
		require.NoError(t, connector.Dial(""))
		acceptedStatus := []connectivity.State{
			connectivity.Ready,
			connectivity.Idle,
		}

		assert.Contains(t, acceptedStatus, connector.conn.GetState())
		assert.Equal(t, 1, fakeFactoryCalled)
		assert.Equal(t, int64(0), clientFake.pingCalled.Load())
		connector.Close()
	})

	t.Run("grpc connection should be ready when socket is listening", func(t *testing.T) {
		fakeFactoryCalled := 0
		clientFake := &fakeClient{}
		fakeFactory := func(grpc.ClientConnInterface) *fakeClient {
			fakeFactoryCalled++
			return clientFake
		}

		const fakeSocketPath = "/tmp/socket.sock"
		connector := NewGRPCConnector(fakeSocketPath, fakeFactory)

		listener, err := net.Listen("unix", fakeSocketPath)
		require.NoError(t, err)
		defer listener.Close()

		require.NoError(t, connector.Dial(""), grpc.WithBlock())
		defer connector.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		assert.True(t, connector.conn.WaitForStateChange(ctx, connectivity.Idle))
		// could be in a transient failure for short time window.
		if connector.conn.GetState() == connectivity.TransientFailure {
			assert.True(t, connector.conn.WaitForStateChange(ctx, connectivity.TransientFailure))
		}
		// https://grpc.github.io/grpc/core/md_doc_connectivity-semantics-and-api.html
		notAcceptedStatus := []connectivity.State{
			connectivity.TransientFailure,
			connectivity.Idle,
			connectivity.Shutdown,
		}

		assert.NotContains(t, notAcceptedStatus, connector.conn.GetState())
	})
}
