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

	"github.com/pkg/errors"
	"google.golang.org/grpc"

	middleware "github.com/dapr/components-contrib/middleware"

	"github.com/dapr/dapr/pkg/components"
)

type (
	// UnaryMiddleware is a GRPC unary middleware component definition.
	UnaryMiddleware struct {
		Name          string
		FactoryMethod UnaryFactoryMethod
	}

	// Registry is the interface for callers to get registered GRPC middleware.
	Registry interface {
		RegisterUnary(components ...UnaryMiddleware)
		CreateUnary(name, version string, metadata middleware.Metadata) (grpc.UnaryServerInterceptor, error)
	}

	grpcMiddlewareRegistry struct {
		unaryMiddleware map[string]UnaryFactoryMethod
	}

	// UnaryFactoryMethod is the method creating middleware from metadata.
	UnaryFactoryMethod func(metadata middleware.Metadata) (grpc.UnaryServerInterceptor, error)
)

// NewUnary creates a Middleware.
func NewUnary(name string, factoryMethod UnaryFactoryMethod) UnaryMiddleware {
	return UnaryMiddleware{
		Name:          name,
		FactoryMethod: factoryMethod,
	}
}

// NewRegistry returns a new GRPC middleware registry.
func NewRegistry() Registry {
	return &grpcMiddlewareRegistry{
		unaryMiddleware: map[string]UnaryFactoryMethod{},
	}
}

// Register registers one or more new GRPC unary middlewares.
func (p *grpcMiddlewareRegistry) RegisterUnary(components ...UnaryMiddleware) {
	for _, component := range components {
		p.unaryMiddleware[createFullName(component.Name)] = component.FactoryMethod
	}
}

// Create instantiates a GRPC middleware based on `name`.
func (p *grpcMiddlewareRegistry) CreateUnary(name, version string, metadata middleware.Metadata) (grpc.UnaryServerInterceptor, error) {
	if method, ok := p.getUnaryMiddleware(name, version); ok {
		mid, err := method(metadata)
		if err != nil {
			return nil, errors.Errorf("error creating GRPC middleware %s/%s: %s", name, version, err)
		}
		return mid, nil
	}
	return nil, errors.Errorf("GRPC middleware %s/%s has not been registered", name, version)
}

func (p *grpcMiddlewareRegistry) getUnaryMiddleware(name, version string) (UnaryFactoryMethod, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	middlewareFn, ok := p.unaryMiddleware[nameLower+"/"+versionLower]
	if ok {
		return middlewareFn, true
	}
	if components.IsInitialVersion(versionLower) {
		middlewareFn, ok = p.unaryMiddleware[nameLower]
	}
	return middlewareFn, ok
}

func createFullName(name string) string {
	return strings.ToLower("middleware.grpc." + name)
}
