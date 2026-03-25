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
package dnslookup

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func startGRPCServerOn(t *testing.T, addr string) (actualAddr string, stop func()) {
	t.Helper()

	lis, err := net.Listen("tcp", addr)
	require.NoError(t, err)

	srv := grpc.NewServer()
	healthpb.RegisterHealthServer(srv, health.NewServer())

	go srv.Serve(lis)

	return lis.Addr().String(), func() { srv.Stop() }
}

func TestIntegration_ReconnectAfterServerRestart(t *testing.T) {
	// Start server 1 on 127.0.0.1 with a random port.
	srv1Addr, stopSrv1 := startGRPCServerOn(t, "127.0.0.1:0")
	_, port, err := net.SplitHostPort(srv1Addr)
	require.NoError(t, err)

	// Start server 2 on [::1] (IPv6 loopback) with the same port. We use ::1
	// instead of 127.0.0.2 because macOS only binds 127.0.0.1 to the loopback
	// interface (Linux routes all of 127.0.0.0/8). IPv6 loopback is available
	// on both platforms and binds the same port without conflict since it's a
	// different address family.
	_, stopSrv2 := startGRPCServerOn(t, net.JoinHostPort("::1", port))
	defer stopSrv2()

	var resolveCount atomic.Int32
	var currentAddr atomic.Value
	currentAddr.Store("127.0.0.1")

	conn, err := New(Options{
		Address: net.JoinHostPort("placement.local", port),
		GRPCOptions: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
		resolver: func(ctx context.Context, host string) ([]string, error) {
			resolveCount.Add(1)
			return []string{currentAddr.Load().(string)}, nil
		},
	})
	require.NoError(t, err)

	grpcConn, err := conn.Connect(t.Context())
	require.NoError(t, err)

	healthClient := healthpb.NewHealthClient(grpcConn)
	ctx1, cancel1 := context.WithTimeout(t.Context(), time.Second)
	defer cancel1()
	_, err = healthClient.Check(ctx1, &healthpb.HealthCheckRequest{})
	require.NoError(t, err)
	grpcConn.Close()

	// Simulate server restart: stop server 1 and update DNS to return the new
	// IP (::1). The connector should re-resolve DNS and connect to server 2
	// immediately without hanging on the stale 127.0.0.1 address.
	stopSrv1()
	currentAddr.Store("::1")

	startTime := time.Now()
	grpcConn, err = conn.Connect(t.Context())
	elapsed := time.Since(startTime)
	require.NoError(t, err)

	assert.Less(t, elapsed, 2*time.Second,
		"reconnect should be fast after DNS re-resolve, not hang on stale IP")
	assert.Equal(t, net.JoinHostPort("::1", port), conn.Address())

	healthClient = healthpb.NewHealthClient(grpcConn)
	ctx2, cancel2 := context.WithTimeout(t.Context(), time.Second)
	defer cancel2()
	_, err = healthClient.Check(ctx2, &healthpb.HealthCheckRequest{})
	require.NoError(t, err)
	grpcConn.Close()

	assert.Equal(t, int32(2), resolveCount.Load())
}

func TestIntegration_AlwaysResolvesOnConnect(t *testing.T) {
	srvAddr, stopSrv := startGRPCServerOn(t, "127.0.0.1:0")
	defer stopSrv()
	_, port, err := net.SplitHostPort(srvAddr)
	require.NoError(t, err)

	var resolveCount atomic.Int32

	conn, err := New(Options{
		Address: net.JoinHostPort("placement.local", port),
		GRPCOptions: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
		resolver: func(ctx context.Context, host string) ([]string, error) {
			resolveCount.Add(1)
			return []string{"127.0.0.1"}, nil
		},
	})
	require.NoError(t, err)

	for range 5 {
		grpcConn, cerr := conn.Connect(t.Context())
		require.NoError(t, cerr)
		grpcConn.Close()
	}

	assert.Equal(t, int32(5), resolveCount.Load())
}
