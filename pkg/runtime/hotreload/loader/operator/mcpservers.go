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

package operator

import (
	"context"
	"encoding/json"
	"fmt"

	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
)

type mcpservers struct {
	operatorpb.Operator_MCPServerUpdateClient
}

//nolint:unused
func (m *mcpservers) list(ctx context.Context, opclient operatorpb.OperatorClient, ns, _ string) ([][]byte, error) {
	resp, err := opclient.ListMCPServers(ctx, &operatorpb.ListMCPServersRequest{
		Namespace: ns,
	})
	if err != nil {
		return nil, err
	}

	return resp.GetMcpServers(), nil
}

//nolint:unused
func (m *mcpservers) close() error {
	if m.Operator_MCPServerUpdateClient != nil {
		return m.CloseSend()
	}
	return nil
}

//nolint:unused
func (m *mcpservers) recv(_ context.Context) (*loader.Event[mcpserverapi.MCPServer], error) {
	event, err := m.Recv()
	if err != nil {
		return nil, err
	}

	var server mcpserverapi.MCPServer
	if err := json.Unmarshal(event.GetMcpServer(), &server); err != nil {
		return nil, fmt.Errorf("failed to deserialize MCPServer: %w", err)
	}

	return &loader.Event[mcpserverapi.MCPServer]{
		Resource: server,
		Type:     event.GetType(),
	}, nil
}

//nolint:unused
func (m *mcpservers) establish(ctx context.Context, opclient operatorpb.OperatorClient, ns, _ string) error {
	stream, err := opclient.MCPServerUpdate(ctx, &operatorpb.MCPServerUpdateRequest{
		Namespace: ns,
	})
	if err != nil {
		return err
	}

	m.Operator_MCPServerUpdateClient = stream
	return nil
}
