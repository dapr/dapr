// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"fmt"

	middleware "github.com/dapr/components-contrib/middleware"
	grpc_middleware "github.com/dapr/dapr/pkg/middleware/grpc"
)

type (
	// Middleware is a gRPC middleware component definition.
	Middleware struct {
		Name          string
		FactoryMethod func(metadata middleware.Metadata) grpc_middleware.Middleware
	}

	// Registry is the interface for callers to get registered gRPC middleware
	Registry interface {
		Register(components ...Middleware)
		Create(name string, metadata middleware.Metadata) (grpc_middleware.Middleware, error)
	}

	grpcMiddlewareRegistry struct {
		middleware map[string]func(middleware.Metadata) grpc_middleware.Middleware
	}
)

// New creates a Middleware.
func New(name string, factoryMethod func(metadata middleware.Metadata) grpc_middleware.Middleware) Middleware {
	return Middleware{
		Name:          name,
		FactoryMethod: factoryMethod,
	}
}

// NewRegistry returns a new gRPC middleware registry.
func NewRegistry() Registry {
	return &grpcMiddlewareRegistry{
		middleware: map[string]func(middleware.Metadata) grpc_middleware.Middleware{},
	}
}

// Register registers one or more new gRPC middlewares.
func (p *grpcMiddlewareRegistry) Register(components ...Middleware) {
	for _, component := range components {
		p.middleware[createFullName(component.Name)] = component.FactoryMethod
	}
}

// Create instantiates a gRPC middleware based on `name`.
func (p *grpcMiddlewareRegistry) Create(name string, metadata middleware.Metadata) (grpc_middleware.Middleware, error) {
	if method, ok := p.middleware[name]; ok {
		return method(metadata), nil
	}
	return nil, fmt.Errorf("gRPC middleware %s has not been registered", name)
}

func createFullName(name string) string {
	return fmt.Sprintf("middleware.grpc.%s", name)
}
