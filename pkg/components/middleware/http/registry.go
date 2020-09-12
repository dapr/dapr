// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"fmt"

	middleware "github.com/dapr/components-contrib/middleware"
	http_middleware "github.com/dapr/dapr/pkg/middleware/http"
	"github.com/pkg/errors"
)

type (
	// Middleware is a HTTP middleware component definition.
	Middleware struct {
		Name          string
		FactoryMethod func(metadata middleware.Metadata) http_middleware.Middleware
	}

	// Registry is the interface for callers to get registered HTTP middleware
	Registry interface {
		Register(components ...Middleware)
		Create(name string, metadata middleware.Metadata) (http_middleware.Middleware, error)
	}

	httpMiddlewareRegistry struct {
		middleware map[string]func(middleware.Metadata) http_middleware.Middleware
	}
)

// New creates a Middleware.
func New(name string, factoryMethod func(metadata middleware.Metadata) http_middleware.Middleware) Middleware {
	return Middleware{
		Name:          name,
		FactoryMethod: factoryMethod,
	}
}

// NewRegistry returns a new HTTP middleware registry.
func NewRegistry() Registry {
	return &httpMiddlewareRegistry{
		middleware: map[string]func(middleware.Metadata) http_middleware.Middleware{},
	}
}

// Register registers one or more new HTTP middlewares.
func (p *httpMiddlewareRegistry) Register(components ...Middleware) {
	for _, component := range components {
		p.middleware[createFullName(component.Name)] = component.FactoryMethod
	}
}

// Create instantiates a HTTP middleware based on `name`.
func (p *httpMiddlewareRegistry) Create(name string, metadata middleware.Metadata) (http_middleware.Middleware, error) {
	if method, ok := p.middleware[name]; ok {
		return method(metadata), nil
	}
	return nil, errors.Errorf("HTTP middleware %s has not been registered", name)
}

func createFullName(name string) string {
	return fmt.Sprintf("middleware.http.%s", name)
}
