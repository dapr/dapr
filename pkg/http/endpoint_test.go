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
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"

	"github.com/dapr/dapr/pkg/config"
)

const (
	healthzEndpoint         = "healthz"
	healthzOutboundEndpoint = "healthz/outbound"
)

func TestAPIAllowlist(t *testing.T) {
	t.Run("state allowed", func(t *testing.T) {
		allowed := config.APIAccessRules{
			{
				Name:     "state",
				Version:  "v1.0",
				Protocol: "http",
			},
			{
				Name:     "state",
				Version:  "v1.0-alpha1",
				Protocol: "http",
			},
		}.ForProtocol("http")

		a := &api{}
		eps := a.constructStateEndpoints()

		for _, e := range eps {
			valid := e.IsAllowed(allowed, nil)
			assert.True(t, valid)
		}

		allOtherEndpoints := []Endpoint{}
		allOtherEndpoints = append(allOtherEndpoints, a.constructActorEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructBindingsEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructDirectMessagingEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructMetadataEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructPubSubEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructSecretEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructShutdownEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructHealthzEndpoints()...)

		for _, e := range allOtherEndpoints {
			valid := e.IsAllowed(allowed, nil)
			switch e.Route {
			// healthz endpoints are always allowed
			case healthzEndpoint, healthzOutboundEndpoint:
				assert.True(t, valid)
			default:
				assert.False(t, valid)
			}
		}
	})

	t.Run("state denied", func(t *testing.T) {
		denied := config.APIAccessRules{
			{
				Name:     "state",
				Version:  "v1.0",
				Protocol: "http",
			},
			{
				Name:     "state",
				Version:  "v1.0-alpha1",
				Protocol: "http",
			},
		}.ForProtocol("http")

		a := &api{}
		eps := a.constructStateEndpoints()

		for _, e := range eps {
			valid := e.IsAllowed(nil, denied)
			assert.False(t, valid, e.Route)
		}

		allOtherEndpoints := []Endpoint{}
		allOtherEndpoints = append(allOtherEndpoints, a.constructActorEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructBindingsEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructDirectMessagingEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructMetadataEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructPubSubEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructSecretEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructShutdownEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructHealthzEndpoints()...)

		for _, e := range allOtherEndpoints {
			valid := e.IsAllowed(nil, denied)
			assert.True(t, valid, e.Route)
		}
	})

	t.Run("publish allowed", func(t *testing.T) {
		allowed := config.APIAccessRules{
			{
				Name:     "publish",
				Version:  "v1.0",
				Protocol: "http",
			},
			{
				Name:     "publish",
				Version:  "v1.0-alpha1",
				Protocol: "http",
			},
		}.ForProtocol("http")

		a := &api{}
		eps := a.constructPubSubEndpoints()

		for _, e := range eps {
			valid := e.IsAllowed(allowed, nil)
			assert.True(t, valid)
		}

		allOtherEndpoints := []Endpoint{}
		allOtherEndpoints = append(allOtherEndpoints, a.constructActorEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructBindingsEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructDirectMessagingEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructMetadataEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructStateEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructSecretEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructShutdownEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructHealthzEndpoints()...)

		for _, e := range allOtherEndpoints {
			valid := e.IsAllowed(allowed, nil)
			switch e.Route {
			// healthz endpoints are always allowed
			case healthzEndpoint, healthzOutboundEndpoint:
				assert.True(t, valid)
			default:
				assert.False(t, valid)
			}
		}
	})

	t.Run("invoke allowed", func(t *testing.T) {
		allowed := config.APIAccessRules{
			{
				Name:     "invoke",
				Version:  "v1.0",
				Protocol: "http",
			},
		}.ForProtocol("http")

		a := &api{}
		eps := a.constructDirectMessagingEndpoints()

		for _, e := range eps {
			valid := e.IsAllowed(allowed, nil)
			assert.True(t, valid)
		}

		allOtherEndpoints := []Endpoint{}
		allOtherEndpoints = append(allOtherEndpoints, a.constructActorEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructBindingsEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructPubSubEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructMetadataEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructStateEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructSecretEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructShutdownEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructHealthzEndpoints()...)

		for _, e := range allOtherEndpoints {
			valid := e.IsAllowed(allowed, nil)
			switch e.Route {
			case healthzEndpoint, healthzOutboundEndpoint:
				assert.True(t, valid)
			default:
				assert.False(t, valid)
			}
		}
	})

	t.Run("bindings allowed", func(t *testing.T) {
		allowed := config.APIAccessRules{
			{
				Name:     "bindings",
				Version:  "v1.0",
				Protocol: "http",
			},
		}.ForProtocol("http")

		a := &api{}
		eps := a.constructBindingsEndpoints()

		for _, e := range eps {
			valid := e.IsAllowed(allowed, nil)
			assert.True(t, valid)
		}

		allOtherEndpoints := []Endpoint{}
		allOtherEndpoints = append(allOtherEndpoints, a.constructActorEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructDirectMessagingEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructPubSubEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructMetadataEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructStateEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructSecretEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructShutdownEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructHealthzEndpoints()...)

		for _, e := range allOtherEndpoints {
			valid := e.IsAllowed(allowed, nil)
			switch e.Route {
			// healthz endpoints are always allowed
			case healthzEndpoint, healthzOutboundEndpoint:
				assert.True(t, valid)
			default:
				assert.False(t, valid)
			}
		}
	})

	t.Run("metadata allowed", func(t *testing.T) {
		allowed := config.APIAccessRules{
			{
				Name:     "metadata",
				Version:  "v1.0",
				Protocol: "http",
			},
		}.ForProtocol("http")

		a := &api{}
		eps := a.constructMetadataEndpoints()

		for _, e := range eps {
			valid := e.IsAllowed(allowed, nil)
			assert.True(t, valid)
		}

		allOtherEndpoints := []Endpoint{}
		allOtherEndpoints = append(allOtherEndpoints, a.constructActorEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructDirectMessagingEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructPubSubEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructBindingsEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructStateEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructSecretEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructShutdownEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructHealthzEndpoints()...)

		for _, e := range allOtherEndpoints {
			valid := e.IsAllowed(allowed, nil)
			switch e.Route {
			// healthz endpoints are always allowed
			case healthzEndpoint, healthzOutboundEndpoint:
				assert.True(t, valid)
			default:
				assert.False(t, valid)
			}
		}
	})

	t.Run("secrets allowed", func(t *testing.T) {
		allowed := config.APIAccessRules{
			{
				Name:     "secrets",
				Version:  "v1.0",
				Protocol: "http",
			},
		}.ForProtocol("http")

		a := &api{}
		eps := a.constructSecretEndpoints()

		for _, e := range eps {
			valid := e.IsAllowed(allowed, nil)
			assert.True(t, valid)
		}

		allOtherEndpoints := []Endpoint{}
		allOtherEndpoints = append(allOtherEndpoints, a.constructActorEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructDirectMessagingEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructPubSubEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructBindingsEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructStateEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructMetadataEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructShutdownEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructHealthzEndpoints()...)

		for _, e := range allOtherEndpoints {
			valid := e.IsAllowed(allowed, nil)
			switch e.Route {
			// healthz endpoints are always allowed
			case healthzEndpoint, healthzOutboundEndpoint:
				assert.True(t, valid)
			default:
				assert.False(t, valid)
			}
		}
	})

	t.Run("shutdown allowed", func(t *testing.T) {
		allowed := config.APIAccessRules{
			{
				Name:     "shutdown",
				Version:  "v1.0",
				Protocol: "http",
			},
		}.ForProtocol("http")

		a := &api{}
		eps := a.constructShutdownEndpoints()

		for _, e := range eps {
			valid := e.IsAllowed(allowed, nil)
			assert.True(t, valid)
		}

		allOtherEndpoints := []Endpoint{}
		allOtherEndpoints = append(allOtherEndpoints, a.constructActorEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructDirectMessagingEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructPubSubEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructBindingsEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructStateEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructMetadataEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructSecretEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructHealthzEndpoints()...)

		for _, e := range allOtherEndpoints {
			valid := e.IsAllowed(allowed, nil)
			switch e.Route {
			// healthz endpoints are always allowed
			case healthzEndpoint, healthzOutboundEndpoint:
				assert.True(t, valid)
			default:
				assert.False(t, valid)
			}
		}
	})

	t.Run("non-overlapping denylist and allowlist", func(t *testing.T) {
		allowed := config.APIAccessRules{
			{
				Name:     "invoke",
				Version:  "v1.0",
				Protocol: "http",
			},
		}.ForProtocol("http")

		denied := config.APIAccessRules{
			{
				Name:     "state",
				Version:  "v1.0",
				Protocol: "http",
			},
			{
				Name:     "state",
				Version:  "v1.0-alpha1",
				Protocol: "http",
			},
		}.ForProtocol("http")

		a := &api{}
		allEndpoints := []Endpoint{}
		allEndpoints = append(allEndpoints, a.constructDirectMessagingEndpoints()...)
		allEndpoints = append(allEndpoints, a.constructActorEndpoints()...)
		allEndpoints = append(allEndpoints, a.constructBindingsEndpoints()...)
		allEndpoints = append(allEndpoints, a.constructStateEndpoints()...)
		allEndpoints = append(allEndpoints, a.constructMetadataEndpoints()...)
		allEndpoints = append(allEndpoints, a.constructPubSubEndpoints()...)
		allEndpoints = append(allEndpoints, a.constructSecretEndpoints()...)
		allEndpoints = append(allEndpoints, a.constructShutdownEndpoints()...)
		allEndpoints = append(allEndpoints, a.constructHealthzEndpoints()...)

		for _, e := range allEndpoints {
			valid := e.IsAllowed(allowed, denied)
			switch {
			// healthz endpoints are always allowed
			case e.Route == healthzEndpoint, e.Route == healthzOutboundEndpoint:
				assert.True(t, valid, e.Route)
			case strings.HasPrefix(e.Route, "invoke"):
				assert.True(t, valid, e.Route)
			default:
				assert.False(t, valid, e.Route)
			}
		}
	})

	t.Run("overlapping denylist and allowlist", func(t *testing.T) {
		allowed := config.APIAccessRules{
			{
				Name:     "state",
				Version:  "v1.0",
				Protocol: "http",
			},
			{
				Name:     "state",
				Version:  "v1.0-alpha1",
				Protocol: "http",
			},
		}.ForProtocol("http")

		denied := config.APIAccessRules{
			{
				Name:     "state",
				Version:  "v1.0",
				Protocol: "http",
			},
		}.ForProtocol("http")

		a := &api{}
		allEndpoints := []Endpoint{}
		allEndpoints = append(allEndpoints, a.constructDirectMessagingEndpoints()...)
		allEndpoints = append(allEndpoints, a.constructActorEndpoints()...)
		allEndpoints = append(allEndpoints, a.constructBindingsEndpoints()...)
		allEndpoints = append(allEndpoints, a.constructStateEndpoints()...)
		allEndpoints = append(allEndpoints, a.constructMetadataEndpoints()...)
		allEndpoints = append(allEndpoints, a.constructPubSubEndpoints()...)
		allEndpoints = append(allEndpoints, a.constructSecretEndpoints()...)
		allEndpoints = append(allEndpoints, a.constructShutdownEndpoints()...)
		allEndpoints = append(allEndpoints, a.constructHealthzEndpoints()...)

		for _, e := range allEndpoints {
			valid := e.IsAllowed(allowed, denied)
			switch {
			// healthz endpoints are always allowed
			case e.Route == healthzEndpoint, e.Route == healthzOutboundEndpoint:
				assert.True(t, valid, e.Route)
			// Only alpha APIs are allowed
			case strings.HasPrefix(e.Route, "state") && e.Version != "v1.0":
				assert.True(t, valid, e.Route)
			default:
				assert.False(t, valid, e.Route)
			}
		}
	})

	t.Run("no rules, all endpoints allowed", func(t *testing.T) {
		a := &api{}
		eps := a.APIEndpoints()

		for _, e := range eps {
			valid := e.IsAllowed(nil, nil)
			assert.True(t, valid)
		}
	})

	t.Run("no rules, all handlers exist", func(t *testing.T) {
		s := server{}

		a := &api{}
		eps := a.APIEndpoints()

		router := s.getRouter(eps)
		r := &fasthttp.RequestCtx{
			Request: fasthttp.Request{},
		}

		for _, e := range eps {
			path := fmt.Sprintf("/%s/%s", e.Version, e.Route)
			for _, m := range e.Methods {
				handler, ok := router.Lookup(m, path, r)
				assert.NotNil(t, handler)
				assert.True(t, ok)
			}
		}
	})

	t.Run("allowlist router handler mismatch protocol, all handlers exist", func(t *testing.T) {
		s := server{
			apiSpec: config.APISpec{
				Allowed: []config.APIAccessRule{
					{
						Name:     "state",
						Version:  "v1.0",
						Protocol: "grpc",
					},
				},
			},
		}

		a := &api{}
		eps := a.APIEndpoints()

		router := s.getRouter(eps)
		r := &fasthttp.RequestCtx{
			Request: fasthttp.Request{},
		}

		for _, e := range eps {
			path := fmt.Sprintf("/%s/%s", e.Version, e.Route)
			for _, m := range e.Methods {
				handler, ok := router.Lookup(m, path, r)
				assert.NotNil(t, handler)
				assert.True(t, ok)
			}
		}
	})

	t.Run("denylist router handler mismatch protocol, all handlers exist", func(t *testing.T) {
		s := server{
			apiSpec: config.APISpec{
				Denied: []config.APIAccessRule{
					{
						Name:     "state",
						Version:  "v1.0",
						Protocol: "grpc",
					},
				},
			},
		}

		a := &api{}
		eps := a.APIEndpoints()

		router := s.getRouter(eps)
		r := &fasthttp.RequestCtx{
			Request: fasthttp.Request{},
		}

		for _, e := range eps {
			path := fmt.Sprintf("/%s/%s", e.Version, e.Route)
			for _, m := range e.Methods {
				handler, ok := router.Lookup(m, path, r)
				assert.NotNil(t, handler)
				assert.True(t, ok)
			}
		}
	})

	t.Run("router handler rules applied, only allowed handlers exist", func(t *testing.T) {
		s := server{
			apiSpec: config.APISpec{
				Allowed: []config.APIAccessRule{
					{
						Version:  "v1.0",
						Name:     "state",
						Protocol: "http",
					},
				},
			},
		}

		a := &api{}
		eps := a.APIEndpoints()

		router := s.getRouter(eps)
		r := &fasthttp.RequestCtx{
			Request: fasthttp.Request{},
		}

		for _, e := range eps {
			path := fmt.Sprintf("/%s/%s", e.Version, e.Route)
			for _, m := range e.Methods {
				handler, ok := router.Lookup(m, path, r)

				if strings.Index(e.Route, "state") == 0 {
					assert.NotNil(t, handler)
					assert.True(t, ok)
				} else {
					assert.Nil(t, handler)
					assert.False(t, ok)
				}
			}
		}
	})
}
