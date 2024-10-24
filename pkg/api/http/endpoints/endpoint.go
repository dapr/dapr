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

package endpoints

import (
	"net/http"
	"strings"
)

// Endpoint is a collection of route information for an Dapr API.
type Endpoint struct {
	Methods  []string
	Route    string
	Version  string
	Group    *EndpointGroup // Endpoint group, used for allowlisting
	Handler  http.HandlerFunc
	Settings EndpointSettings
}

// EndpointSettings contains settings for the endpoint.
type EndpointSettings struct {
	Name          string // Method name, used in logging and for other purposes
	IsFallback    bool   // Endpoint is used as fallback when the method or URL isn't found
	AlwaysAllowed bool   // Endpoint is always allowed regardless of API access rules
	IsHealthCheck bool   // Mark endpoint as healthcheck - for API logging purposes
}

// IsAllowed returns true if the endpoint is allowed given the API allowlist/denylist.
func (endpoint Endpoint) IsAllowed(allowedAPIs map[string]struct{}, deniedAPIs map[string]struct{}) bool {
	// If the endpoint is always allowed, return true
	if endpoint.Settings.AlwaysAllowed {
		return true
	}

	// First, check the denylist
	if len(deniedAPIs) > 0 && endpointMatchesAPIAccessRule(endpoint, deniedAPIs) {
		return false
	}

	// Now check the allowlist if present
	if len(allowedAPIs) == 0 {
		return true
	}

	return endpointMatchesAPIAccessRule(endpoint, allowedAPIs)
}

func endpointMatchesAPIAccessRule(endpoint Endpoint, rules map[string]struct{}) (ok bool) {
	var key string

	// First, check using the "new method", where we use the endpoint's group configuration
	if endpoint.Group != nil {
		key = string(endpoint.Group.Version) + "/" + string(endpoint.Group.Name)
		_, ok = rules[key]
		if ok {
			return true
		}
	}

	// Try with the "legacy" method, where we matched the path
	for k := range rules {
		if strings.HasPrefix(endpoint.Version+"/"+endpoint.Route, k) {
			return true
		}
	}

	return false
}
