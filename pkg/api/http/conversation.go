/*
Copyright 2022 The Dapr Authors
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

	"github.com/dapr/dapr/pkg/api/http/endpoints"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func (a *api) constructConversationEndpoints() []endpoints.Endpoint {
	return []endpoints.Endpoint{
		{
			Methods: []string{http.MethodGet},
			Route:   "conversation/{name}/conversate",
			Version: apiVersionV1alpha1,
			Group: &endpoints.EndpointGroup{
				Name:                 endpoints.EndpointGroupConversation,
				Version:              endpoints.EndpointGroupVersion1alpha1,
				AppendSpanAttributes: nil,
			},
			Handler: a.onConverseAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "Converse",
			},
		},
	}
}

func (a *api) onConverseAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.ConverseAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.ConversationAlpha1Request, *runtimev1pb.ConversationAlpha1Response]{
			ProtoResponseEmitUnpopulated: false,
		},
	)
}
