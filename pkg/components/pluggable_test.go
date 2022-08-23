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

package components

import (
	"context"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/connectivity"
)

func TestPluggableConnect(t *testing.T) {
	const (
		fakeName          = "name"
		fakeType          = "type"
		fakeVersion       = "v1"
		fakeComponentName = "component"
	)

	fakePluggable := Pluggable{
		Name:    fakeName,
		Type:    fakeType,
		Version: fakeVersion,
	}
	t.Run("grpc connection should be idle when the process is listening to the socket", func(t *testing.T) {
		conn, err := fakePluggable.Connect(fakeComponentName)
		assert.Nil(t, err)
		assert.Equal(t, connectivity.Idle, conn.GetState())
		conn.Close()
	})

	t.Run("grpc connection should be ready when socket is listening", func(t *testing.T) {
		defer os.Clearenv()
		os.Setenv(daprSocketFolderEnvVar, "/tmp")

		var grpcConnectionGroup, netUnixSocketListenGroup sync.WaitGroup
		grpcConnectionGroup.Add(1)
		netUnixSocketListenGroup.Add(1)
		go func() {
			socket := fakePluggable.socketPathFor(fakeComponentName)
			listener, err := net.Listen("unix", socket)
			assert.Nil(t, err)
			grpcConnectionGroup.Wait()
			if listener != nil {
				listener.Close()
			}
			netUnixSocketListenGroup.Done()
		}()
		conn, err := fakePluggable.Connect(fakeComponentName)
		assert.Nil(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		assert.True(t, conn.WaitForStateChange(ctx, connectivity.Idle))
		notAcceptedStatus := []connectivity.State{
			connectivity.TransientFailure,
			connectivity.Idle,
			connectivity.Shutdown,
		}

		assert.NotContains(t, notAcceptedStatus, conn.GetState())

		grpcConnectionGroup.Done()
		netUnixSocketListenGroup.Wait()
		conn.Close()
	})
}
