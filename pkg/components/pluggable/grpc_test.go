/*
Copyright 2021 The Dapr Authors
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
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dapr/dapr/pkg/components"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/protobuf/types/known/emptypb"
)

type fakeClient struct {
	pingCalled atomic.Int64
}

func (f *fakeClient) Ping(context.Context, *emptypb.Empty, ...grpc.CallOption) (*emptypb.Empty, error) {
	f.pingCalled.Add(1)
	return &emptypb.Empty{}, nil
}

func TestGRPCConnector(t *testing.T) {
	const (
		fakeName          = "name"
		fakeType          = "type"
		fakeVersion       = "v1"
		fakeComponentName = "component"
	)

	fakePluggable := components.Pluggable{
		Name:    fakeName,
		Type:    fakeType,
		Version: fakeVersion,
	}

	t.Run("grpc connection should be idle when the process is listening to the socket", func(t *testing.T) {
		fakeFactoryCalled := 0
		clientFake := &fakeClient{}
		fakeFactory := func(grpc.ClientConnInterface) *fakeClient {
			fakeFactoryCalled++
			return clientFake
		}
		connector := NewGRPCConnector(fakePluggable, fakeFactory)
		require.NoError(t, connector.Dial(fakeComponentName))
		assert.Equal(t, connectivity.Idle, connector.conn.GetState())
		assert.Equal(t, 1, fakeFactoryCalled)
		assert.Equal(t, int64(1), clientFake.pingCalled.Load())
		connector.Close()
	})

	t.Run("grpc connection should be ready when socket is listening", func(t *testing.T) {
		fakeFactoryCalled := 0
		clientFake := &fakeClient{}
		fakeFactory := func(grpc.ClientConnInterface) *fakeClient {
			fakeFactoryCalled++
			return clientFake
		}
		connector := NewGRPCConnector(fakePluggable, fakeFactory)

		defer os.Clearenv()
		os.Setenv(daprSocketFolderEnvVar, "/tmp")

		var grpcConnectionGroup, netUnixSocketListenGroup sync.WaitGroup
		grpcConnectionGroup.Add(1)
		netUnixSocketListenGroup.Add(1)
		go func() {
			socket := connector.socketPathFor(fakeComponentName)
			listener, err := net.Listen("unix", socket)
			assert.Nil(t, err)
			grpcConnectionGroup.Wait()
			if listener != nil {
				listener.Close()
			}
			netUnixSocketListenGroup.Done()
		}()
		require.NoError(t, connector.Dial(fakeComponentName))
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		assert.True(t, connector.conn.WaitForStateChange(ctx, connectivity.Idle))
		notAcceptedStatus := []connectivity.State{
			connectivity.TransientFailure,
			connectivity.Idle,
			connectivity.Shutdown,
		}

		assert.NotContains(t, notAcceptedStatus, connector.conn.GetState())

		grpcConnectionGroup.Done()
		netUnixSocketListenGroup.Wait()
		connector.Close()
	})
}
