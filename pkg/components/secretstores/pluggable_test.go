//go:build !windows
// +build !windows

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

package secretstores

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"testing"

	guuid "github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	contribMetadata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/dapr/pkg/components/pluggable"
	proto "github.com/dapr/dapr/pkg/proto/components/v1"
	testingGrpc "github.com/dapr/dapr/pkg/testing/grpc"
	"github.com/dapr/kit/logger"
)

var testLogger = logger.NewLogger("secretstores-pluggable-logger")

type server struct {
	proto.UnimplementedSecretStoreServer
	initCalled          atomic.Int64
	onInitCalled        func(*proto.SecretStoreInitRequest)
	initErr             error
	featuresCalled      atomic.Int64
	featuresErr         error
	getSecretCalled     atomic.Int64
	onGetSecret         func(*proto.GetSecretRequest)
	getSecretErr        error
	bulkGetSecretCalled atomic.Int64
	onBulkGetSecret     func(*proto.BulkGetSecretRequest)
	bulkGetSecretErr    error
	pingCalled          atomic.Int64
	pingErr             error
}

func (s *server) Init(ctx context.Context, req *proto.SecretStoreInitRequest) (*proto.SecretStoreInitResponse, error) {
	s.initCalled.Add(1)
	if s.onInitCalled != nil {
		s.onInitCalled(req)
	}
	return &proto.SecretStoreInitResponse{}, s.initErr
}

func (s *server) Features(ctx context.Context, req *proto.FeaturesRequest) (*proto.FeaturesResponse, error) {
	s.featuresCalled.Add(1)
	return &proto.FeaturesResponse{}, s.featuresErr
}

func (s *server) Get(ctx context.Context, req *proto.GetSecretRequest) (*proto.GetSecretResponse, error) {
	s.getSecretCalled.Add(1)
	if s.onGetSecret != nil {
		s.onGetSecret(req)
	}
	return &proto.GetSecretResponse{}, s.getSecretErr
}

func (s *server) BulkGet(ctx context.Context, req *proto.BulkGetSecretRequest) (*proto.BulkGetSecretResponse, error) {
	s.bulkGetSecretCalled.Add(1)
	if s.onBulkGetSecret != nil {
		s.onBulkGetSecret(req)
	}
	return &proto.BulkGetSecretResponse{}, s.bulkGetSecretErr
}

func (s *server) Ping(ctx context.Context, req *proto.PingRequest) (*proto.PingResponse, error) {
	s.pingCalled.Add(1)
	return &proto.PingResponse{}, s.pingErr
}

func TestComponentCalls(t *testing.T) {
	getSecretStores := testingGrpc.TestServerFor(testLogger, func(s *grpc.Server, svc *server) {
		proto.RegisterSecretStoreServer(s, svc)
	}, func(cci grpc.ClientConnInterface) *grpcSecretStore {
		client := proto.NewSecretStoreClient(cci)
		secretStore := fromConnector(testLogger, pluggable.NewGRPCConnector("/tmp/socket.sock", proto.NewSecretStoreClient))
		secretStore.Client = client
		return secretStore
	})

	t.Run("init should call grpc init and populate features", func(t *testing.T) {
		const (
			fakeName          = "name"
			fakeType          = "type"
			fakeVersion       = "v1"
			fakeComponentName = "component"
			fakeSocketFolder  = "/tmp"
		)

		uniqueID := guuid.New().String()
		socket := fmt.Sprintf("%s/%s.sock", fakeSocketFolder, uniqueID)
		defer os.Remove(socket)

		connector := pluggable.NewGRPCConnector(socket, proto.NewSecretStoreClient)
		defer connector.Close()

		listener, err := net.Listen("unix", socket)
		require.NoError(t, err)
		defer listener.Close()
		s := grpc.NewServer()
		srv := &server{}
		proto.RegisterSecretStoreServer(s, srv)
		go func() {
			if serveErr := s.Serve(listener); serveErr != nil {
				testLogger.Debugf("failed to serve: %v", serveErr)
			}
		}()

		secretStore := fromConnector(testLogger, connector)
		err = secretStore.Init(context.Background(), secretstores.Metadata{
			Base: contribMetadata.Base{},
		})
		require.NoError(t, err)
		assert.Equal(t, int64(1), srv.initCalled.Load())
		assert.Equal(t, int64(1), srv.featuresCalled.Load())
	})

	t.Run("features should return the secret store features", func(t *testing.T) {
		secretStore, cleanup, err := getSecretStores(&server{})
		require.NoError(t, err)
		defer cleanup()
		features := secretStore.Features()
		assert.Empty(t, features)
		secretStore.features = []secretstores.Feature{secretstores.FeatureMultipleKeyValuesPerSecret}
		assert.NotEmpty(t, secretStore.Features())
		assert.Equal(t, secretstores.FeatureMultipleKeyValuesPerSecret, secretStore.Features()[0])
	})
	t.Run("get secret should call grpc get secret", func(t *testing.T) {
		key := "secretName"
		errStr := "secret not found"
		svc := &server{
			onGetSecret: func(req *proto.GetSecretRequest) {
				assert.Equal(t, key, req.GetKey())
			},
			getSecretErr: errors.New(errStr),
		}

		secretStore, cleanup, err := getSecretStores(svc)
		require.NoError(t, err)
		defer cleanup()

		resp, err := secretStore.GetSecret(context.Background(), secretstores.GetSecretRequest{
			Name: key,
		})
		assert.Equal(t, int64(1), svc.getSecretCalled.Load())
		str := err.Error()
		assert.Equal(t, err.Error(), str)
		assert.Equal(t, secretstores.GetSecretResponse{}, resp)
	})

	t.Run("bulk get secret should call grpc bulk get secret", func(t *testing.T) {
		errStr := "bulk get secret error"
		svc := &server{
			onBulkGetSecret: func(req *proto.BulkGetSecretRequest) {
				// no-op
			},
			bulkGetSecretErr: errors.New(errStr),
		}
		gSecretStores, cleanup, err := getSecretStores(svc)
		require.NoError(t, err)
		defer cleanup()

		resp, err := gSecretStores.BulkGetSecret(context.Background(), secretstores.BulkGetSecretRequest{})
		assert.Equal(t, int64(1), svc.bulkGetSecretCalled.Load())
		str := err.Error()
		assert.Equal(t, err.Error(), str)
		assert.Equal(t, secretstores.BulkGetSecretResponse{}, resp)
	})

	t.Run("ping should not return an err when grpc not returns an error", func(t *testing.T) {
		svc := &server{}
		gSecretStores, cleanup, err := getSecretStores(svc)
		require.NoError(t, err)
		defer cleanup()

		err = gSecretStores.Ping()

		require.NoError(t, err)
		assert.Equal(t, int64(1), svc.pingCalled.Load())
	})

	t.Run("ping should return an err when grpc returns an error", func(t *testing.T) {
		svc := &server{
			pingErr: errors.New("fake-ping-err"),
		}
		gSecretStores, cleanup, err := getSecretStores(svc)
		require.NoError(t, err)
		defer cleanup()

		err = gSecretStores.Ping()

		require.Error(t, err)
		assert.Equal(t, int64(1), svc.pingCalled.Load())
	})
}
