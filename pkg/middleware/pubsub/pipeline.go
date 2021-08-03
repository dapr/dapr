// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsub

import (
	"context"
	"github.com/dapr/components-contrib/pubsub"
)

type RequestHandler func(ctx context.Context, msg *pubsub.NewMessage)
type Middleware func(next RequestHandler) RequestHandler

type Pipeline struct {
	Middlewares []Middleware
}

func (p Pipeline) Apply(handler RequestHandler) RequestHandler {
	for i := len(p.Middlewares) - 1; i >= 0; i-- {
		handler = p.Middlewares[i](handler)
	}
	return handler
}

func (p *Pipeline) UseMiddleware(middleware Middleware) {
	p.Middlewares = append(p.Middlewares, middleware)
}
