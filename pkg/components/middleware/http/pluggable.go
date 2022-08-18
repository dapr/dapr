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
	m "github.com/dapr/components-contrib/middleware"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/components/pluggable"
	"github.com/dapr/dapr/pkg/middleware/http"
	"github.com/valyala/fasthttp"
)

// NewMiddlewareFromPluggable creates a new httpMiddleware from a given pluggable component.
func NewMiddlewareFromPluggable(pc pluggable.Component) Middleware {
	return Middleware{
		Names: []string{pc.Name},
		FactoryMethod: func(metadata m.Metadata) (http.Middleware, error) {
			return func(h fasthttp.RequestHandler) fasthttp.RequestHandler {
				return func(ctx *fasthttp.RequestCtx) {}
			}, nil
		},
	}
}

func init() {
	pluggable.Register(components.HTTPMiddleware, NewMiddlewareFromPluggable)
}
