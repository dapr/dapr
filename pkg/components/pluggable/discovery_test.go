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
	"errors"
	"net"
	"os"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeReflectService struct {
	listServicesCalled atomic.Int64
	listServicesResp   []string
	listServicesErr    error
	onResetCalled      func()
}

func (f *fakeReflectService) ListServices() ([]string, error) {
	f.listServicesCalled.Add(1)
	return f.listServicesResp, f.listServicesErr
}

func (f *fakeReflectService) Reset() {
	f.onResetCalled()
}

type fakeGrpcCloser struct {
	grpcConnectionCloser
	onCloseCalled func()
}

func (f *fakeGrpcCloser) Close() error {
	f.onCloseCalled()
	return nil
}

func TestServiceCallback(t *testing.T) {
	t.Run("callback should be called when service ref is registered", func(t *testing.T) {
		const fakeComponentName, fakeServiceName = "fake-comp", "fake-svc"
		called := 0
		AddServiceDiscoveryCallback(fakeServiceName, func(name string, _ GRPCConnectionDialer) {
			called++
			assert.Equal(t, fakeComponentName, name)
		})
		callback([]service{{protoRef: fakeServiceName, componentName: fakeComponentName}})
		assert.Equal(t, 1, called)
	})
}

func TestConnectionCloser(t *testing.T) {
	t.Run("connection closer should call grpc close and client reset", func(t *testing.T) {
		const close, reset = "close", "reset"
		callOrder := []string{}
		fakeCloser := &fakeGrpcCloser{
			onCloseCalled: func() {
				callOrder = append(callOrder, close)
			},
		}
		fakeService := &fakeReflectService{
			onResetCalled: func() {
				callOrder = append(callOrder, reset)
			},
		}
		closer := reflectServiceConnectionCloser(fakeCloser, fakeService)
		closer()
		assert.Len(t, callOrder, 2)
		assert.Equal(t, []string{reset, close}, callOrder)
	})
}

func TestComponentDiscovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		return
	}
	t.Run("add service callback should add a new entry when called", func(t *testing.T) {
		AddServiceDiscoveryCallback("fake", func(string, GRPCConnectionDialer) {})
		assert.NotEmpty(t, onServiceDiscovered)
	})
	t.Run("serviceDiscovery should return empty services if directory not exists", func(t *testing.T) {
		services, err := serviceDiscovery(func(string) (reflectServiceClient, func(), error) {
			return &fakeReflectService{}, func() {}, nil
		})
		require.NoError(t, err)
		assert.Empty(t, services)
	})
	t.Run("serviceDiscovery should not connect to service that isn't a unix domain socket", func(t *testing.T) {
		const fakeSocketFolder, pattern = "/tmp/test", "fake"
		err := os.MkdirAll(fakeSocketFolder, os.ModePerm)
		defer os.RemoveAll(fakeSocketFolder)
		require.NoError(t, err)
		t.Setenv(SocketFolderEnvVar, fakeSocketFolder)
		_, err = os.CreateTemp(fakeSocketFolder, pattern)
		require.NoError(t, err)

		services, err := serviceDiscovery(func(string) (reflectServiceClient, func(), error) {
			return &fakeReflectService{}, func() {}, nil
		})
		require.NoError(t, err)
		assert.Empty(t, services)
	})
	t.Run("serviceDiscovery should return an error when reflect client factory returns an error", func(t *testing.T) {
		const fakeSocketFolder = "/tmp/test"
		err := os.MkdirAll(fakeSocketFolder, os.ModePerm)
		defer os.RemoveAll(fakeSocketFolder)
		require.NoError(t, err)
		t.Setenv(SocketFolderEnvVar, fakeSocketFolder)

		const fileName = fakeSocketFolder + "/socket1234.sock"
		listener, err := net.Listen("unix", fileName)
		require.NoError(t, err)
		defer listener.Close()

		reflectService := &fakeReflectService{}

		_, err = serviceDiscovery(func(string) (reflectServiceClient, func(), error) {
			return nil, nil, errors.New("fake-err")
		})
		require.Error(t, err)
		assert.Equal(t, int64(0), reflectService.listServicesCalled.Load())
	})
	t.Run("serviceDiscovery should return an error when list services return an error", func(t *testing.T) {
		const fakeSocketFolder = "/tmp/test"
		err := os.MkdirAll(fakeSocketFolder, os.ModePerm)
		defer os.RemoveAll(fakeSocketFolder)
		require.NoError(t, err)
		t.Setenv(SocketFolderEnvVar, fakeSocketFolder)

		const fileName = fakeSocketFolder + "/socket1234.sock"
		listener, err := net.Listen("unix", fileName)
		require.NoError(t, err)
		defer listener.Close()

		reflectService := &fakeReflectService{
			listServicesErr: errors.New("fake-err"),
		}

		_, err = serviceDiscovery(func(string) (reflectServiceClient, func(), error) {
			return reflectService, func() {}, nil
		})
		require.Error(t, err)
		assert.Equal(t, int64(1), reflectService.listServicesCalled.Load())
	})
	t.Run("serviceDiscovery should return all services list", func(t *testing.T) {
		const fakeSocketFolder = "/tmp/test"
		err := os.MkdirAll(fakeSocketFolder, os.ModePerm)
		defer os.RemoveAll(fakeSocketFolder)
		require.NoError(t, err)
		t.Setenv(SocketFolderEnvVar, fakeSocketFolder)

		subFolder := fakeSocketFolder + "/subfolder"
		err = os.MkdirAll(subFolder, os.ModePerm) // should skip subfolders
		defer os.RemoveAll(subFolder)
		require.NoError(t, err)

		const fileName = fakeSocketFolder + "/socket1234.sock"
		listener, err := net.Listen("unix", fileName)
		require.NoError(t, err)
		defer listener.Close()

		svcList := []string{"svcA", "svcB"}
		reflectService := &fakeReflectService{
			listServicesResp: svcList,
		}

		services, err := serviceDiscovery(func(string) (reflectServiceClient, func(), error) {
			return reflectService, func() {}, nil
		})
		require.NoError(t, err)
		assert.Len(t, services, len(svcList))
		assert.Equal(t, int64(1), reflectService.listServicesCalled.Load())
	})
}

func TestRemoveExt(t *testing.T) {
	t.Run("remove ext should remove file extension when it has one", func(t *testing.T) {
		assert.Equal(t, "a", removeExt("a.sock"))
	})
	t.Run("remove ext should not change file name when it has no extension", func(t *testing.T) {
		assert.Equal(t, "a", removeExt("a"))
	})
}

func TestGetSocketFolder(t *testing.T) {
	t.Run("get socket folder should use default when env var is not set", func(t *testing.T) {
		assert.Equal(t, defaultSocketFolder, GetSocketFolderPath())
	})
	t.Run("get socket folder should use env var when set", func(t *testing.T) {
		const fakeSocketFolder = "/tmp"
		t.Setenv(SocketFolderEnvVar, fakeSocketFolder)
		assert.Equal(t, fakeSocketFolder, GetSocketFolderPath())
	})
}
