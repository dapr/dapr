// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"strings"

	"github.com/pkg/errors"

	middleware "github.com/dapr/components-contrib/middleware"

	"github.com/dapr/dapr/pkg/components"
	http_middleware "github.com/dapr/dapr/pkg/middleware/http"
)

type (
	// Middleware is a HTTP middleware component definition.
	Middleware struct {
		Name          string
		FactoryMethod FactoryMethod
	}

	// Registry is the interface for callers to get registered HTTP middleware.
	Registry interface {
		Register(components ...Middleware)
		Create(name, version string, metadata middleware.Metadata) (http_middleware.Middleware, error)
	}

	httpMiddlewareRegistry struct {
		middleware map[string]FactoryMethod
	}

	// FactoryMethod is the method creating middleware from metadata.
	FactoryMethod func(metadata middleware.Metadata) (http_middleware.Middleware, error)
)

// New creates a Middleware.
func New(name string, factoryMethod FactoryMethod) Middleware {
	return Middleware{
		Name:          name,
		FactoryMethod: factoryMethod,
	}
}

// NewRegistry returns a new HTTP middleware registry.
func NewRegistry() Registry {
	return &httpMiddlewareRegistry{
		middleware: map[string]FactoryMethod{},
	}
}

// Register registers one or more new HTTP middlewares.
func (p *httpMiddlewareRegistry) Register(components ...Middleware) {
	for _, component := range components {
		p.middleware[createFullName(component.Name)] = component.FactoryMethod
	}
}

// Create instantiates a HTTP middleware based on `name`.
func (p *httpMiddlewareRegistry) Create(name, version string, metadata middleware.Metadata) (http_middleware.Middleware, error) {
	if method, ok := p.getMiddleware(name, version); ok {
		mid, err := method(metadata)
		if err != nil {
			return nil, errors.Errorf("error creating HTTP middleware %s/%s: %s", name, version, err)
		}
		return mid, nil
	}
	return nil, errors.Errorf("HTTP middleware %s/%s has not been registered", name, version)
}

func (p *httpMiddlewareRegistry) getMiddleware(name, version string) (FactoryMethod, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	middlewareFn, ok := p.middleware[nameLower+"/"+versionLower]
	if ok {
		return middlewareFn, true
	}
	if components.IsInitialVersion(versionLower) {
		middlewareFn, ok = p.middleware[nameLower]
	}
	return middlewareFn, ok
}

func createFullName(name string) string {
	return strings.ToLower("middleware.http." + name)
}
