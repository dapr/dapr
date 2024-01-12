/*
Copyright 2023 The Dapr Authors
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
	"github.com/dapr/dapr/pkg/messages"
)

var endpointGroupHealthzV1 = &endpoints.EndpointGroup{
	Name:                 endpoints.EndpointGroupHealth,
	Version:              endpoints.EndpointGroupVersion1,
	AppendSpanAttributes: nil, // TODO
}

func (a *api) constructHealthzEndpoints() []endpoints.Endpoint {
	return []endpoints.Endpoint{
		{
			Methods: []string{http.MethodGet},
			Route:   "healthz",
			Version: apiVersionV1,
			Group:   endpointGroupHealthzV1,
			Handler: a.onGetHealthz,
			Settings: endpoints.EndpointSettings{
				Name:          "Healthz",
				AlwaysAllowed: true,
				IsHealthCheck: true,
			},
		},
		{
			Methods: []string{http.MethodGet},
			Route:   "healthz/outbound",
			Version: apiVersionV1,
			Group:   endpointGroupHealthzV1,
			Handler: a.onGetOutboundHealthz,
			Settings: endpoints.EndpointSettings{
				Name:          "HealthzOutbound",
				AlwaysAllowed: true,
				IsHealthCheck: true,
			},
		},
	}
}

func (a *api) onGetHealthz(w http.ResponseWriter, r *http.Request) {
	if !a.readyStatus {
		msg := messages.ErrHealthNotReady
		respondWithError(w, msg)
		log.Debug(msg)
		return
	}

	// If we have an "appid" parameter in the query string, we will return an error if the ID of this app is not the value of the requested "appid"
	// This is used by some components (e.g. Consul nameresolver) to check if the app was replaced with a different one
	qs := r.URL.Query()
	if qs.Has("appid") && qs.Get("appid") != a.universal.AppID() {
		msg := messages.ErrHealthAppIDNotMatch
		respondWithError(w, msg)
		log.Debug(msg)
		return
	}

	respondWithEmpty(w)
}

func (a *api) onGetOutboundHealthz(w http.ResponseWriter, r *http.Request) {
	if !a.outboundReadyStatus {
		msg := messages.ErrOutboundHealthNotReady
		respondWithError(w, msg)
		log.Debug(msg)
		return
	}

	respondWithEmpty(w)
}
