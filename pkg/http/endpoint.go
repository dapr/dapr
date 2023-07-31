/*
Copyright 2021 The Dapr Authors
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

	"github.com/valyala/fasthttp"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/utils/nethttpadaptor"
)

// Endpoint is a collection of route information for an Dapr API.
type Endpoint struct {
	Methods               []string
	Route                 string
	Version               string
	IsFallback            bool // Endpoint is used as fallback when the method or URL isn't found
	KeepWildcardUnescaped bool // Keeps the wildcard param in path unescaped
	FastHTTPHandler       fasthttp.RequestHandler
	Handler               http.HandlerFunc
	AlwaysAllowed         bool // Endpoint is always allowed regardless of API access rules
	IsHealthCheck         bool // Mark endpoint as healthcheck - for API logging purposes
}

// GetHandler returns the handler for the endpoint.
// TODO: Remove this when FastHTTP support is removed.
func (endpoint Endpoint) GetHandler() http.HandlerFunc {
	// Sanity-check to catch development-time errors
	if (endpoint.Handler == nil && endpoint.FastHTTPHandler == nil) || (endpoint.Handler != nil && endpoint.FastHTTPHandler != nil) {
		panic("one and only one of Handler and FastHTTPHandler must be defined for endpoint " + endpoint.Route)
	}

	if endpoint.Handler != nil {
		return endpoint.Handler
	}

	return nethttpadaptor.NewNetHTTPHandlerFunc(endpoint.FastHTTPHandler)
}

// IsAllowed returns true if the endpoint is allowed given the API allowlist/denylist.
func (endpoint Endpoint) IsAllowed(allowedAPIs []config.APIAccessRule, deniedAPIs []config.APIAccessRule) bool {
	// If the endpoint is always allowed, return true
	if endpoint.AlwaysAllowed {
		return true
	}

	// First, check the denylist
	if len(deniedAPIs) > 0 {
		for _, rule := range deniedAPIs {
			if endpointMatchesAPIAccessRule(endpoint, rule) {
				return false
			}
		}
	}

	// Now check the allowlist if present
	if len(allowedAPIs) == 0 {
		return true
	}

	for _, rule := range allowedAPIs {
		if endpointMatchesAPIAccessRule(endpoint, rule) {
			return true
		}
	}

	return false
}

func endpointMatchesAPIAccessRule(endpoint Endpoint, rule config.APIAccessRule) bool {
	return endpoint.Version == rule.Version && strings.HasPrefix(endpoint.Route, rule.Name)
}
