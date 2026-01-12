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

package manager

import (
	"net"
	"testing"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestNewManager(t *testing.T) {
	t.Run("with self hosted", func(t *testing.T) {
		m := NewManager(nil, modes.StandaloneMode, &AppChannelConfig{})
		assert.NotNil(t, m)
		assert.Equal(t, modes.StandaloneMode, m.mode)
	})

	t.Run("with kubernetes", func(t *testing.T) {
		m := NewManager(nil, modes.KubernetesMode, &AppChannelConfig{})
		assert.NotNil(t, m)
		assert.Equal(t, modes.KubernetesMode, m.mode)
	})
}

func TestGetAppClient_Scaling(t *testing.T) {
	// Start a mock gRPC server
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer lis.Close()

	server := grpc.NewServer()
	go func() {
		if err := server.Serve(lis); err != nil && err != grpc.ErrServerStopped {
			t.Errorf("Server failed: %v", err)
		}
	}()
	defer server.Stop()

	port := lis.Addr().(*net.TCPAddr).Port

	// Create a Manager with a dummy config
	mgr := NewManager(nil, modes.StandaloneMode, &AppChannelConfig{
		Port:        port,
		BaseAddress: "127.0.0.1",
		TracingSpec: config.TracingSpec{},
	})

	// Inject a create function that we can track if needed,
	// but the default one should work fine since we have a real listener.

	// We want to verify that calling GetAppClient 101 times creates 2 connections
	// because the first one hit the 100 stream limit.

	conns := make([]grpc.ClientConnInterface, 0, 101)
	teardowns := make([]func(bool), 0, 101)

	for i := 0; i < 101; i++ {
		c, teardown, err := mgr.GetAppClient()
		require.NoError(t, err)
		conns = append(conns, c)
		teardowns = append(teardowns, teardown)
	}

	uniqueConns := make(map[grpc.ClientConnInterface]struct{})
	for _, c := range conns {
		uniqueConns[c] = struct{}{}
	}

	// Should have exactly 2 unique connections
	assert.Equal(t, 2, len(uniqueConns), "Expected 2 unique connections for 101 streams")

	// Release them all
	for _, td := range teardowns {
		td(false)
	}

	// Next call should reuse one of them
	c, td, err := mgr.GetAppClient()
	require.NoError(t, err)
	assert.Contains(t, uniqueConns, c)
	td(false)
}

func TestGetAppClient_BaselineComparison(t *testing.T) {
	// This test specifically checks that we can handle more than 100 "active" clients

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer lis.Close()

	go func() {
		s := grpc.NewServer()
		s.Serve(lis)
	}()

	port := lis.Addr().(*net.TCPAddr).Port

	mgr := NewManager(nil, modes.StandaloneMode, &AppChannelConfig{
		Port:        port,
		BaseAddress: "127.0.0.1",
	})

	// Simulate 250 requests staying open (like long-running streams)
	conns := make([]grpc.ClientConnInterface, 250)
	for i := 0; i < 250; i++ {
		c, _, err := mgr.GetAppClient()
		require.NoError(t, err)
		conns[i] = c
	}

	uniqueConns := make(map[grpc.ClientConnInterface]struct{})
	for _, c := range conns {
		uniqueConns[c] = struct{}{}
	}

	assert.Equal(t, 3, len(uniqueConns), "Expected 3 unique connections for 250 streams")
}
