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

	"github.com/go-chi/chi/v5"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func (a *api) constructSecretEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{http.MethodGet},
			Route:   "secrets/{secretStoreName}/bulk",
			Version: apiVersionV1,
			Handler: a.onBulkGetSecretHandler(),
		},
		{
			Methods: []string{http.MethodGet},
			Route:   "secrets/{secretStoreName}/{key}",
			Version: apiVersionV1,
			Handler: a.onGetSecretHandler(),
		},
	}
}

func (a *api) onGetSecretHandler() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.GetSecret,
		UniversalHTTPHandlerOpts[*runtimev1pb.GetSecretRequest, *runtimev1pb.GetSecretResponse]{
			InModifier: func(r *http.Request, in *runtimev1pb.GetSecretRequest) (*runtimev1pb.GetSecretRequest, error) {
				in.StoreName = chi.URLParam(r, secretStoreNameParam)
				in.Key = chi.URLParam(r, secretNameParam)
				in.Metadata = getMetadataFromRequest(r)
				return in, nil
			},
			OutModifier: func(out *runtimev1pb.GetSecretResponse) (any, error) {
				// If the data is nil, return nil
				if out == nil || out.Data == nil {
					return nil, nil
				}

				// Return just the data property
				return out.Data, nil
			},
		},
	)
}

func (a *api) onBulkGetSecretHandler() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.GetBulkSecret,
		UniversalHTTPHandlerOpts[*runtimev1pb.GetBulkSecretRequest, *runtimev1pb.GetBulkSecretResponse]{
			InModifier: func(r *http.Request, in *runtimev1pb.GetBulkSecretRequest) (*runtimev1pb.GetBulkSecretRequest, error) {
				in.StoreName = chi.URLParam(r, secretStoreNameParam)
				in.Metadata = getMetadataFromRequest(r)
				return in, nil
			},
			OutModifier: func(out *runtimev1pb.GetBulkSecretResponse) (any, error) {
				// If the data is nil, return nil
				if out == nil || out.Data == nil {
					return nil, nil
				}

				var secrets map[string]map[string]string
				secrets = make(map[string]map[string]string, len(out.Data))
				// Return just the secrets as map
				for secretKey, secret := range out.Data {
					secrets[secretKey] = secret.Secrets
				}

				return secrets, nil
			},
		},
	)
}
