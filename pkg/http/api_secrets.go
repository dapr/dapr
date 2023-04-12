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
	nethttp "net/http"

	"github.com/valyala/fasthttp"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func (a *api) constructSecretEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{nethttp.MethodGet},
			Route:   "secrets/{secretStoreName}/bulk",
			Version: apiVersionV1,
			Handler: a.onBulkGetSecretHandler(),
		},
		{
			Methods: []string{nethttp.MethodGet},
			Route:   "secrets/{secretStoreName}/{key}",
			Version: apiVersionV1,
			Handler: a.onGetSecretHandler(),
		},
	}
}

func (a *api) onGetSecretHandler() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.GetSecret,
		UniversalFastHTTPHandlerOpts[*runtimev1pb.GetSecretRequest, *runtimev1pb.GetSecretResponse]{
			InModifier: func(reqCtx *fasthttp.RequestCtx, in *runtimev1pb.GetSecretRequest) (*runtimev1pb.GetSecretRequest, error) {
				in.StoreName = reqCtx.UserValue(secretStoreNameParam).(string)
				in.Key = reqCtx.UserValue(secretNameParam).(string)
				in.Metadata = getMetadataFromRequest(reqCtx)
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

func (a *api) onBulkGetSecretHandler() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.GetBulkSecret,
		UniversalFastHTTPHandlerOpts[*runtimev1pb.GetBulkSecretRequest, *runtimev1pb.GetBulkSecretResponse]{
			InModifier: func(reqCtx *fasthttp.RequestCtx, in *runtimev1pb.GetBulkSecretRequest) (*runtimev1pb.GetBulkSecretRequest, error) {
				in.StoreName = reqCtx.UserValue(secretStoreNameParam).(string)
				in.Metadata = getMetadataFromRequest(reqCtx)
				return in, nil
			},
		},
	)
}
