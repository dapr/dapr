// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsub

import (
	middleware "github.com/dapr/components-contrib/middleware"
	"github.com/dapr/dapr/pkg/components"
	pubsub_middleware "github.com/dapr/dapr/pkg/middleware/pubsub"
	"github.com/pkg/errors"
	"strings"
)

type (
	// Middleware is a Pubsub middleware component definition.
	Middleware struct {
		Name          string
		FactoryMethod func(metadata middleware.Metadata) pubsub_middleware.Middleware
	}

	// Registry is the interface for callers to get registered Pubsub middleware.
	Registry interface {
		Register(components ...Middleware)
		Create(name, version string, metadata middleware.Metadata) (pubsub_middleware.Middleware, error)
	}

	pubsubMiddlewareRegistry struct {
		middleware map[string]func(middleware.Metadata) pubsub_middleware.Middleware
	}
)

// New creates a Middleware.
func New(name string, factoryMethod func(metadata middleware.Metadata) pubsub_middleware.Middleware) Middleware {
	return Middleware{
		Name:          name,
		FactoryMethod: factoryMethod,
	}
}

// NewRegistry returns a new Pubsub middleware registry.
func NewRegistry() Registry {
	return &pubsubMiddlewareRegistry{
		middleware: map[string]func(middleware.Metadata) pubsub_middleware.Middleware{},
	}
}

// Register registers one or more new Pubsub middlewares.
func (p *pubsubMiddlewareRegistry) Register(components ...Middleware) {
	for _, component := range components {
		p.middleware[createFullName(component.Name)] = component.FactoryMethod
	}
}

// Create instantiates a Pubsub middleware based on `name`.
func (p *pubsubMiddlewareRegistry) Create(name, version string, metadata middleware.Metadata) (pubsub_middleware.Middleware, error) {
	if method, ok := p.getMiddleware(name, version); ok {
		return method(metadata), nil
	}
	return nil, errors.Errorf("Pubsub middleware %s/%s has not been registered", name, version)
}

func (p *pubsubMiddlewareRegistry) getMiddleware(name, version string) (func(middleware.Metadata) pubsub_middleware.Middleware, bool) {
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
	return strings.ToLower("middleware.pubsub." + name)
}
