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

	grpcretry "github.com/grpc-ecosystem/go-grpc-middleware/retry"

	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	config "github.com/dapr/dapr/pkg/config/modes"
	"github.com/dapr/dapr/pkg/internal/loader"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

type mcpservers struct {
	config    config.KubernetesConfig
	client    operatorv1pb.OperatorClient
	namespace string
	podName   string
}

// NewMCPServers returns a new Kubernetes loader for MCPServer resources.
func NewMCPServers(opts Options) loader.Loader[mcpserverapi.MCPServer] {
	return &mcpservers{
		config:    opts.Config,
		client:    opts.Client,
		namespace: opts.Namespace,
		podName:   opts.PodName,
	}
}

// Load fetches MCPServer resources from the Operator for this namespace.
// If the MCPServer CRD is not installed, the Operator's client.List returns
// an error which is propagated to the caller.
func (m *mcpservers) Load(ctx context.Context) ([]mcpserverapi.MCPServer, error) {
	resp, err := m.client.ListMCPServers(ctx, &operatorv1pb.ListMCPServersRequest{
		Namespace: m.namespace,
	}, grpcretry.WithMax(operatorMaxRetries), grpcretry.WithPerRetryTimeout(operatorCallTimeout))
	if err != nil {
		log.Errorf("Error listing MCP servers: %v", err)
		return nil, err
	}

	items := resp.GetMcpServers()
	if len(items) == 0 {
		return nil, nil
	}

	servers := make([]mcpserverapi.MCPServer, len(items))
	for i, raw := range items {
		var s mcpserverapi.MCPServer
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("error deserializing MCP server: %w", err)
		}
		servers[i] = s
	}

	return servers, nil
}
