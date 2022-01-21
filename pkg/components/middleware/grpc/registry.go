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

package grpc

import (
	"strings"

	middleware "github.com/dapr/components-contrib/middleware"

	"github.com/dapr/dapr/pkg/components"
	grpc_middleware "github.com/dapr/dapr/pkg/middleware/grpc"
)

const (
	UnaryServerMiddlewareTypePrefix = "middleware.grpc.server.unary."
)

type (
	// UnaryServerMiddleware is a GRPC unary server middleware component definition.
	UnaryServerMiddleware struct {
		Name          string
		FactoryMethod UnaryServerFactoryMethod
	}

	// UnaryServerFactoryMethod is the method creating middleware from metadata.
	UnaryServerFactoryMethod func(metadata middleware.Metadata) (grpc_middleware.UnaryServerMiddleware, error)

	// Registry is the interface for callers to get registered GRPC middleware.
	Registry interface {
		RegisterUnaryServer(components ...UnaryServerMiddleware)
		CreateUnaryServer(name, version string, metadata middleware.Metadata) (grpc_middleware.UnaryServerMiddleware, error)

		// Can extend to support other GRPC middleware types (i.e. StreamServer, UnaryClient, UnaryStream)
	}

	grpcMiddlewareRegistry struct {
		unaryServerMiddleware map[string]UnaryServerFactoryMethod
	}
)

// NewRegistry returns a new GRPC middleware registry.
func NewRegistry() Registry {
	return &grpcMiddlewareRegistry{
		unaryServerMiddleware: map[string]UnaryServerFactoryMethod{},
	}
}

// Register registers one or more new GRPC unary server middlewares.
func (p *grpcMiddlewareRegistry) RegisterUnaryServer(components ...UnaryServerMiddleware) {
	for _, component := range components {
		p.unaryServerMiddleware[createFullNameForUnaryServer(component.Name)] = component.FactoryMethod
	}
}

// Create instantiates a GRPC unary server middleware based on `name`.
func (p *grpcMiddlewareRegistry) CreateUnaryServer(name, version string, metadata middleware.Metadata) (grpc_middleware.UnaryServerMiddleware, error) {
	if method, ok := p.getUnaryServerMiddleware(name, version); ok {
		mid, err := method(metadata)
		if err != nil {
			return nil, &ErrUnaryServerBadCreate{Name: name, Version: version, Err: err}
		}
		return mid, nil
	}
	return nil, &ErrUnaryServerNotRegistered{Name: name, Version: version}
}

// NewUnaryServerMiddleware creates a Middleware.
func NewUnaryServerMiddleware(name string, factoryMethod UnaryServerFactoryMethod) UnaryServerMiddleware {
	return UnaryServerMiddleware{
		Name:          name,
		FactoryMethod: factoryMethod,
	}
}

func createFullNameForUnaryServer(name string) string {
	return strings.ToLower(UnaryServerMiddlewareTypePrefix + name)
}

func (p *grpcMiddlewareRegistry) getUnaryServerMiddleware(name, version string) (UnaryServerFactoryMethod, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	middlewareFn, ok := p.unaryServerMiddleware[nameLower+"/"+versionLower]
	if ok {
		return middlewareFn, true
	}
	if components.IsInitialVersion(versionLower) {
		middlewareFn, ok = p.unaryServerMiddleware[nameLower]
	}
	return middlewareFn, ok
}
