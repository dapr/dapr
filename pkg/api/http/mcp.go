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

package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/dapr/dapr/pkg/api/http/endpoints"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func (a *api) constructMCPEndpoints() []endpoints.Endpoint {
	return []endpoints.Endpoint{
		{
			Methods: []string{http.MethodPost},
			Route:   "mcp/{serverName}/tools",
			Version: apiVersionV1alpha1,
			Group: &endpoints.EndpointGroup{
				Name:                 endpoints.EndpointGroupMCP,
				Version:              endpoints.EndpointGroupVersion1alpha1,
				AppendSpanAttributes: nil,
			},
			Handler: a.onListMCPToolsAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "ListMCPTools",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "mcp/{serverName}/tools/call",
			Version: apiVersionV1alpha1,
			Group: &endpoints.EndpointGroup{
				Name:                 endpoints.EndpointGroupMCP,
				Version:              endpoints.EndpointGroupVersion1alpha1,
				AppendSpanAttributes: nil,
			},
			Handler: a.onCallMCPToolAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "CallMCPTool",
			},
		},
	}
}

func (a *api) onListMCPToolsAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.ListMCPToolsAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.ListMCPToolsRequest, *runtimev1pb.ListMCPToolsResponse]{
			InModifier: func(r *http.Request, in *runtimev1pb.ListMCPToolsRequest) (*runtimev1pb.ListMCPToolsRequest, error) {
				in.McpServerName = chi.URLParam(r, "serverName")
				return in, nil
			},
		},
	)
}

func (a *api) onCallMCPToolAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.CallMCPToolAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.CallMCPToolRequest, *runtimev1pb.CallMCPToolResponse]{
			InModifier: func(r *http.Request, in *runtimev1pb.CallMCPToolRequest) (*runtimev1pb.CallMCPToolRequest, error) {
				in.McpServerName = chi.URLParam(r, "serverName")
				return in, nil
			},
		},
	)
}
