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
	"strings"

	grpcMetadata "google.golang.org/grpc/metadata"

	"github.com/dapr/dapr/pkg/api/http/endpoints"
	"github.com/dapr/dapr/pkg/api/universal"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func (a *api) constructShutdownEndpoints() []endpoints.Endpoint {
	return []endpoints.Endpoint{
		{
			Methods: []string{http.MethodPost},
			Route:   "shutdown",
			Version: apiVersionV1,
			Group: &endpoints.EndpointGroup{
				Name:                 endpoints.EndpointGroupShutdown,
				Version:              endpoints.EndpointGroupVersion1,
				AppendSpanAttributes: nil, // TODO
			},
			Handler: a.onShutdown,
			Settings: endpoints.EndpointSettings{
				Name: "Shutdown",
			},
		},
	}
}

func (a *api) onShutdown(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if strings.EqualFold(r.Header.Get(universal.ForceShutdownMetadataKey), "true") {
		ctx = grpcMetadata.NewIncomingContext(ctx, grpcMetadata.Pairs(
			universal.ForceShutdownMetadataKey, "true",
		))
	}

	if _, err := a.universal.Shutdown(ctx, &runtimev1pb.ShutdownRequest{}); err != nil {
		respondWithError(w, err)
		return
	}
	respondWithEmpty(w)
}
