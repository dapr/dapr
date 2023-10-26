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
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/dapr/dapr/pkg/api/http/endpoints"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func (a *api) constructDistributedLockEndpoints() []endpoints.Endpoint {
	return []endpoints.Endpoint{
		{
			Methods: []string{http.MethodPost},
			Route:   "lock/{storeName}",
			Version: apiVersionV1alpha1,
			Group: &endpoints.EndpointGroup{
				Name:                 endpoints.EndpointGroupLock,
				Version:              endpoints.EndpointGroupVersion1alpha1,
				AppendSpanAttributes: nil, // TODO
			},
			Handler: a.onTryLockAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "TryLock",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "unlock/{storeName}",
			Version: apiVersionV1alpha1,
			Group: &endpoints.EndpointGroup{
				Name:                 endpoints.EndpointGroupUnlock,
				Version:              endpoints.EndpointGroupVersion1alpha1,
				AppendSpanAttributes: nil, // TODO
			},
			Handler: a.onUnlockAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "Unlock",
			},
		},
	}
}

func (a *api) onTryLockAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.TryLockAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.TryLockRequest, *runtimev1pb.TryLockResponse]{
			InModifier: func(r *http.Request, in *runtimev1pb.TryLockRequest) (*runtimev1pb.TryLockRequest, error) {
				in.StoreName = chi.URLParam(r, storeNameParam)
				return in, nil
			},
			// We need to emit unpopulated fields in the response
			ProtoResponseEmitUnpopulated: true,
		},
	)
}

func (a *api) onUnlockAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.UnlockAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.UnlockRequest, *runtimev1pb.UnlockResponse]{
			InModifier: func(r *http.Request, in *runtimev1pb.UnlockRequest) (*runtimev1pb.UnlockRequest, error) {
				in.StoreName = chi.URLParam(r, storeNameParam)
				return in, nil
			},
			OutModifier: func(out *runtimev1pb.UnlockResponse) (any, error) {
				// Create the response manually because we want to report the status as a number and not a string
				b, err := protojson.MarshalOptions{
					EmitUnpopulated: true,
					UseEnumNumbers:  true,
				}.Marshal(out)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal response as JSON: %w", err)
				}
				return &UniversalHTTPRawResponse{
					Body:        b,
					ContentType: jsonContentTypeHeader,
				}, nil
			},
		},
	)
}
