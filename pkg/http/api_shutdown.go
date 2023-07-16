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

	"google.golang.org/protobuf/types/known/emptypb"
)

func (a *api) constructShutdownEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{http.MethodPost},
			Route:   "shutdown",
			Version: apiVersionV1,
			Handler: UniversalHTTPHandler(
				a.universal.Shutdown,
				UniversalHTTPHandlerOpts[*emptypb.Empty, *emptypb.Empty]{
					OutModifier: func(out *emptypb.Empty) (any, error) {
						// Nullify the response so status code is 204
						return nil, nil
					},
				},
			),
		},
	}
}
