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

package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/dapr/pkg/api/grpc/metadata"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	securityConsts "github.com/dapr/dapr/pkg/security/consts"
	daprt "github.com/dapr/dapr/pkg/testing"
)

// TODO: Add APIVersion testing

var mockServer *channelt.MockServer

func TestMain(m *testing.M) {
	// Setup
	lis, err := net.Listen("tcp", "127.0.0.1:9998")
	if err != nil {
		log.Fatalf("failed to create listener: %v", err)
	}

	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(metadata.SetMetadataInContextUnary),
		grpc.InTapHandle(metadata.SetMetadataInTapHandle),
	)
	mockServer = &channelt.MockServer{}
	go func() {
		runtimev1pb.RegisterAppCallbackServer(grpcServer, mockServer)
		runtimev1pb.RegisterAppCallbackHealthCheckServer(grpcServer, mockServer)
		grpcServer.Serve(lis)
		if err != nil {
			log.Fatalf("failed to start gRPC server: %v", err)
		}
	}()

	// Run tests
	code := m.Run()

	// Teardown
	grpcServer.Stop()

	os.Exit(code)
}

func createConnection(t *testing.T) *grpc.ClientConn {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	conn, err := grpc.DialContext(ctx, "localhost:9998",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	cancel()
	require.NoError(t, err, "failed to connect to gRPC server")
	return conn
}

func closeConnection(t *testing.T, conn *grpc.ClientConn) {
	err := conn.Close()
	require.NoError(t, err, "failed to close client connection")
}

func TestInvokeMethod(t *testing.T) {
	conn := createConnection(t)
	defer closeConnection(t, conn)
	c := Channel{
		baseAddress:          "localhost:9998",
		appCallbackClient:    runtimev1pb.NewAppCallbackClient(conn),
		conn:                 conn,
		appMetadataToken:     "token1",
		maxRequestBodySizeMB: 4,
	}
	ctx := context.Background()

	t.Run("successful request", func(t *testing.T) {
		req := invokev1.NewInvokeMethodRequest("method").
			WithHTTPExtension(http.MethodPost, "param1=val1&param2=val2")
		defer req.Close()
		response, err := c.InvokeMethod(ctx, req, "")
		require.NoError(t, err)
		defer response.Close()

		assert.Equal(t, "application/json", response.ContentType())

		actual := map[string]string{}
		err = json.NewDecoder(response.RawData()).Decode(&actual)

		require.NoError(t, err)
		assert.Equal(t, "POST", actual["httpverb"])
		assert.Equal(t, "method", actual["method"])
		assert.Equal(t, "token1", actual[securityConsts.APITokenHeader])
		assert.Equal(t, "param1=val1&param2=val2", actual["querystring"])
	})

	t.Run("request body stream errors", func(t *testing.T) {
		req := invokev1.NewInvokeMethodRequest("method").
			WithHTTPExtension(http.MethodPost, "param1=val1&param2=val2").
			WithRawData(&daprt.ErrorReader{})
		defer req.Close()

		response, err := c.InvokeMethod(ctx, req, "")
		require.Error(t, err)
		require.ErrorIs(t, err, io.ErrClosedPipe)
		if response != nil {
			defer response.Close()
		}
	})
}

func TestHealthProbe(t *testing.T) {
	conn := createConnection(t)
	c := Channel{
		baseAddress:          "localhost:9998",
		appCallbackClient:    runtimev1pb.NewAppCallbackClient(conn),
		conn:                 conn,
		appMetadataToken:     "token1",
		maxRequestBodySizeMB: 4,
	}
	ctx := context.Background()

	var (
		success bool
		err     error
	)

	// OK response
	success, err = c.HealthProbe(ctx)
	require.NoError(t, err)
	assert.True(t, success)

	// Non-2xx status code
	mockServer.Error = errors.New("test failure")
	success, err = c.HealthProbe(ctx)
	require.Error(t, err)
	assert.False(t, success)

	// Closed connection
	closeConnection(t, conn)
	success, err = c.HealthProbe(ctx)
	require.Error(t, err)
	assert.False(t, success)
}

func TestCreateLocalChannelWithBaseAddress(t *testing.T) {
	ch := CreateLocalChannel(8080, 1, nil, config.TracingSpec{}, 1024, 1, "my.app")
	assert.Equal(t, "my.app:8080", ch.baseAddress)
}
