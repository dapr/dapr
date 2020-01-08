// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"github.com/dapr/dapr/pkg/config"
	"github.com/valyala/fasthttp"
)

type Middleware func(h fasthttp.RequestHandler) fasthttp.RequestHandler

// HTTPPipeline defines the middleware pipeline to be plugged into Dapr sidecar
type Pipeline struct {
	Handlers []Middleware
}

func BuildHTTPPipeline(spec config.PipelineSpec) (Pipeline, error) {
	return Pipeline{}, nil
}

func (p Pipeline) Apply(handler fasthttp.RequestHandler) fasthttp.RequestHandler {
	for i := len(p.Handlers) - 1; i >= 0; i-- {
		handler = p.Handlers[i](handler)
	}
	return handler
}
