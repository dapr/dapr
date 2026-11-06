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
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/dapr/dapr/pkg/api/http/endpoints"
	"github.com/dapr/dapr/pkg/messages"
)

// notReadyWarnThreshold is how long a not-ready streak that has never reached ready
// must persist before it is escalated to Warn. This keeps a normal startup quiet
// while still surfacing a sidecar that gets stuck and never becomes ready.
const notReadyWarnThreshold = 30 * time.Second

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
	if !a.healthz.IsReady() {
		msg := messages.ErrHealthNotReady.WithFormat(a.healthz.GetUnhealthyTargets())
		respondWithError(w, msg)
		warnOnPersistentNotReady(msg, msg.Message(), &a.healthzEverReady, &a.healthzNotReadySince, &a.healthzNotReadyLogged)
		return
	}
	a.healthzEverReady.Store(true)
	a.healthzNotReadySince.Store(0)
	if a.healthzNotReadyLogged.CompareAndSwap(true, false) {
		log.Info("dapr is ready again")
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
	if !a.outboundHealthz.IsReady() {
		msg := messages.ErrOutboundHealthNotReady
		respondWithError(w, msg)
		warnMsg := fmt.Sprintf("%s: %v", msg.Message(), a.outboundHealthz.GetUnhealthyTargets())
		warnOnPersistentNotReady(msg, warnMsg, &a.outboundEverReady, &a.outboundNotReadySince, &a.outboundNotReadyLogged)
		return
	}
	a.outboundEverReady.Store(true)
	a.outboundNotReadySince.Store(0)
	if a.outboundNotReadyLogged.CompareAndSwap(true, false) {
		log.Info("dapr outbound is ready again")
	}

	respondWithEmpty(w)
}

// warnOnPersistentNotReady logs a not-ready poll at Warn once per not-ready streak,
// either immediately if this is a regression after having been ready before, or once
// the streak has persisted past notReadyWarnThreshold for a target that has never
// reached ready. This keeps a normal startup quiet while still surfacing a sidecar
// that never comes up. debugMsg is logged at Debug while the streak is within the
// threshold and hasn't warned yet; warnMsg is the human-readable message used at Warn.
func warnOnPersistentNotReady(debugMsg error, warnMsg string, everReady *atomic.Bool, since *atomic.Int64, logged *atomic.Bool) {
	if everReady.Load() {
		if logged.CompareAndSwap(false, true) {
			log.Warn(warnMsg)
		}
		return
	}

	now := time.Now()
	start := since.Load()
	if start == 0 {
		since.CompareAndSwap(0, now.UnixNano())
		start = since.Load()
	}

	if now.Sub(time.Unix(0, start)) < notReadyWarnThreshold {
		log.Debug(debugMsg)
		return
	}

	if logged.CompareAndSwap(false, true) {
		log.Warn(warnMsg)
	}
}
