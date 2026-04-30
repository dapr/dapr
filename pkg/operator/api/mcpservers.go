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

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/operator/api/authz"
	loopsclient "github.com/dapr/dapr/pkg/operator/api/loops/client"
	"github.com/dapr/dapr/pkg/operator/api/loops/sender"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

// processMCPServerSecrets resolves any secretKeyRef entries in the transport
// headers and stdio env using the Kubernetes secret store.
func processMCPServerSecrets(ctx context.Context, s *mcpserverapi.MCPServer, namespace string, kubeClient client.Client) error {
	// Only resolve secrets when auth.secretStore is "kubernetes" or unset (defaults to kubernetes).
	secretStore := s.GetSecretStore()
	if secretStore != "" && secretStore != kubernetesSecretStore {
		// Non-kubernetes secret stores are resolved by the sidecar processor, not the operator.
		return nil
	}

	// Resolve header secrets for whichever HTTP transport is configured.
	switch {
	case s.Spec.Endpoint.StreamableHTTP != nil:
		if err := resolveSecretKeyRefs(ctx, s.Spec.Endpoint.StreamableHTTP.Headers, s.Name, "streamableHTTP header", namespace, kubeClient); err != nil {
			return err
		}
	case s.Spec.Endpoint.SSE != nil:
		if err := resolveSecretKeyRefs(ctx, s.Spec.Endpoint.SSE.Headers, s.Name, "sse header", namespace, kubeClient); err != nil {
			return err
		}
	case s.Spec.Endpoint.Stdio != nil:
		if err := resolveSecretKeyRefs(ctx, s.Spec.Endpoint.Stdio.Env, s.Name, "stdio env", namespace, kubeClient); err != nil {
			return err
		}
	}

	return nil
}

// resolveSecretKeyRefs resolves secretKeyRef entries in a slice of NameValuePairs
// using the Kubernetes secret store.
func resolveSecretKeyRefs(ctx context.Context, pairs []commonapi.NameValuePair, serverName, fieldDesc, namespace string, kubeClient client.Client) error {
	for i, pair := range pairs {
		if pair.SecretKeyRef.Name == "" {
			continue
		}
		v, err := getSecret(ctx, pair.SecretKeyRef.Name, namespace, pair.SecretKeyRef, kubeClient)
		if err != nil {
			return fmt.Errorf("error resolving %s secret for MCPServer %q: %w", fieldDesc, serverName, err)
		}
		pairs[i].Value = v
	}
	return nil
}

// ListMCPServers returns all MCPServer resources for the requested namespace.
func (a *apiServer) ListMCPServers(ctx context.Context, in *operatorv1pb.ListMCPServersRequest) (*operatorv1pb.ListMCPServersResponse, error) {
	if _, err := authz.Request(ctx, in.GetNamespace()); err != nil {
		return nil, err
	}

	resp := &operatorv1pb.ListMCPServersResponse{
		McpServers: [][]byte{},
	}

	var list mcpserverapi.MCPServerList
	if err := a.Client.List(ctx, &list, &client.ListOptions{
		Namespace: in.GetNamespace(),
	}); err != nil {
		return nil, fmt.Errorf("error listing MCP servers: %w", err)
	}

	for i := range list.Items {
		item := list.Items[i]
		if err := processMCPServerSecrets(ctx, &item, item.Namespace, a.Client); err != nil {
			log.Warnf("error processing secrets for MCP server %q/%q: %s", item.Namespace, item.Name, err)
			return &operatorv1pb.ListMCPServersResponse{}, err
		}

		b, err := json.Marshal(item)
		if err != nil {
			log.Warnf("error marshalling MCP server %q: %s", item.Name, err)
			continue
		}
		resp.McpServers = append(resp.GetMcpServers(), b)
	}

	return resp, nil
}

// MCPServerUpdate handles streaming MCP server update events for a connected sidecar.
func (a *apiServer) MCPServerUpdate(in *operatorv1pb.MCPServerUpdateRequest, srv operatorv1pb.Operator_MCPServerUpdateServer) error { //nolint:nosnakecase
	if a.closed.Load() {
		return errors.New("server is closed")
	}

	log.Debug("sidecar connected for MCP server updates")

	ctx := srv.Context()

	ch, cancel, err := a.mcpServerInformer.WatchUpdates(ctx, in.GetNamespace())
	if err != nil {
		return err
	}

	stream, err := sender.New(srv)
	if err != nil {
		return err
	}

	c := loopsclient.New(loopsclient.Options[mcpserverapi.MCPServer]{
		EventCh:        ch,
		CancelWatch:    cancel,
		Stream:         stream,
		Namespace:      in.GetNamespace(),
		KubeClient:     a.Client,
		ProcessSecrets: processMCPServerSecrets,
	})
	defer c.CacheLoop()

	if err := c.Run(ctx); err != nil {
		log.Warnf("MCP server client loop ended with error: %s", err)
	}

	return nil
}
