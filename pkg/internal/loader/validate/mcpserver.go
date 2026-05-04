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

package validate

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	daprcrds "github.com/dapr/dapr/charts/dapr"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
)

var mcpServerValidator = NewValidator("MCPServer", daprcrds.MCPServerCRD)

// MCPServerSecurity validates security-sensitive fields on an MCPServer.
// Called after CEL validation.
// Rejects unsafe configurations that the schema alone cannot catch.
func MCPServerSecurity(server *mcpserverapi.MCPServer, kubernetesMode bool) error {
	// Stdio transport: reject in Kubernetes mode
	if server.Spec.Endpoint.Stdio != nil {
		if kubernetesMode {
			return fmt.Errorf("MCPServer %q: stdio transport is not allowed in Kubernetes mode", server.Name)
		}
		if server.Spec.Endpoint.Stdio.Command == "" {
			return fmt.Errorf("MCPServer %q: stdio.command must not be empty", server.Name)
		}
		if strings.Contains(server.Spec.Endpoint.Stdio.Command, "..") {
			return fmt.Errorf("MCPServer %q: stdio.command must not contain path traversal", server.Name)
		}
		for _, env := range server.Spec.Endpoint.Stdio.Env {
			if strings.ContainsAny(env.Name, "=\n\x00") {
				return fmt.Errorf("MCPServer %q: stdio.env name %q contains invalid characters", server.Name, env.Name)
			}
		}
	}

	// HTTP URL validation: only allow http:// and https:// schemes.
	if sh := server.Spec.Endpoint.StreamableHTTP; sh != nil {
		if err := validateMCPURL(server.Name, "streamableHTTP", sh.URL); err != nil {
			return err
		}
	}
	if sse := server.Spec.Endpoint.SSE; sse != nil {
		if err := validateMCPURL(server.Name, "sse", sse.URL); err != nil {
			return err
		}
	}

	return nil
}

func validateMCPURL(serverName, transport, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("MCPServer %q: %s.url is not a valid URL: %w", serverName, transport, err)
	}
	switch u.Scheme {
	case "http", "https":
		// ok
	default:
		return fmt.Errorf("MCPServer %q: %s.url scheme must be http or https, got %q", serverName, transport, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("MCPServer %q: %s.url must have a host", serverName, transport)
	}
	return nil
}

// MCPServer validates an MCPServer against the CEL rules and schema
// constraints embedded in the CRD. This provides the same validation in
// standalone mode that the Kubernetes API server provides via CRD admission.
func MCPServer(ctx context.Context, server *mcpserverapi.MCPServer) error {
	return mcpServerValidator.Validate(ctx, server)
}
