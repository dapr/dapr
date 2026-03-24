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

package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	config "github.com/dapr/dapr/pkg/config/modes"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

// mockOperatorMCP extends mockOperator with ListMCPServers support.
type mockOperatorMCP struct {
	operatorv1pb.UnimplementedOperatorServer
	servers []mcpserverapi.MCPServer
}

func (o *mockOperatorMCP) ListMCPServers(ctx context.Context, in *operatorv1pb.ListMCPServersRequest) (*operatorv1pb.ListMCPServersResponse, error) {
	out := make([][]byte, 0, len(o.servers))
	for _, s := range o.servers {
		b, err := json.Marshal(s)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return &operatorv1pb.ListMCPServersResponse{McpServers: out}, nil
}

func TestLoadMCPServersKubernetes(t *testing.T) {
	t.Run("returns MCP servers from operator", func(t *testing.T) {
		server := mcpserverapi.MCPServer{}
		server.Name = "github"
		server.Spec = mcpserverapi.MCPServerSpec{
			Endpoint: mcpserverapi.MCPEndpoint{
				Transport: mcpserverapi.MCPTransportStreamableHTTP,
				Target:    mcpserverapi.MCPEndpointTarget{URL: "https://api.githubcopilot.com/mcp/"},
			},
		}

		port, _ := freeport.GetFreePort()
		lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		require.NoError(t, err)

		s := grpc.NewServer()
		operatorv1pb.RegisterOperatorServer(s, &mockOperatorMCP{servers: []mcpserverapi.MCPServer{server}})
		errCh := make(chan error, 1)
		t.Cleanup(func() {
			s.Stop()
			require.NoError(t, <-errCh)
		})
		go func() { errCh <- s.Serve(lis) }()

		loader := &mcpservers{
			client:    getOperatorClient(fmt.Sprintf("localhost:%d", port)),
			config:    config.KubernetesConfig{ControlPlaneAddress: fmt.Sprintf("localhost:%d", port)},
			namespace: "default",
		}

		servers, err := loader.Load(t.Context())
		require.NoError(t, err)
		require.Len(t, servers, 1)
		assert.Equal(t, "github", servers[0].Name)
		assert.Equal(t, mcpserverapi.MCPTransportStreamableHTTP, servers[0].Spec.Endpoint.Transport)
		assert.Equal(t, "https://api.githubcopilot.com/mcp/", servers[0].Spec.Endpoint.Target.URL)
	})

	t.Run("empty response returns nil, no error", func(t *testing.T) {
		port, _ := freeport.GetFreePort()
		lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		require.NoError(t, err)

		s := grpc.NewServer()
		operatorv1pb.RegisterOperatorServer(s, &mockOperatorMCP{servers: nil})
		errCh := make(chan error, 1)
		t.Cleanup(func() {
			s.Stop()
			require.NoError(t, <-errCh)
		})
		go func() { errCh <- s.Serve(lis) }()

		loader := &mcpservers{
			client:    getOperatorClient(fmt.Sprintf("localhost:%d", port)),
			config:    config.KubernetesConfig{ControlPlaneAddress: fmt.Sprintf("localhost:%d", port)},
			namespace: "default",
		}

		servers, err := loader.Load(t.Context())
		require.NoError(t, err)
		assert.Nil(t, servers)
	})
}
