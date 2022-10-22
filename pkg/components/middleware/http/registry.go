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
	"strings"

	"github.com/pkg/errors"

	middleware "github.com/dapr/components-contrib/middleware"
	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/components"
	httpMiddleware "github.com/dapr/dapr/pkg/middleware/http"
)

type (
	// Registry is the interface for callers to get registered HTTP middleware.
	Registry struct {
		Logger     logger.Logger
		middleware map[string]func(logger.Logger) FactoryMethod
	}

	// FactoryMethod is the method creating middleware from metadata.
	FactoryMethod func(metadata middleware.Metadata) (httpMiddleware.Middleware, error)
)

// DefaultRegistry is the singleton with the registry.
var DefaultRegistry *Registry

func init() {
	DefaultRegistry = NewRegistry()
}

// NewRegistry returns a new HTTP middleware registry.
func NewRegistry() *Registry {
	return &Registry{
		middleware: map[string]func(logger.Logger) FactoryMethod{},
	}
}

// RegisterComponent adds a new HTTP middleware to the registry.
func (p *Registry) RegisterComponent(componentFactory func(logger.Logger) FactoryMethod, names ...string) {
	for _, name := range names {
		p.middleware[createFullName(name)] = componentFactory
	}
}

// Create instantiates a HTTP middleware based on `name`.
func (p *Registry) Create(name, version string, metadata middleware.Metadata) (httpMiddleware.Middleware, error) {
	if method, ok := p.getMiddleware(name, version); ok {
		mid, err := method(metadata)
		if err != nil {
			return nil, errors.Errorf("error creating HTTP middleware %s/%s: %s", name, version, err)
		}
		return mid, nil
	}
	return nil, errors.Errorf("HTTP middleware %s/%s has not been registered", name, version)
}

func (p *Registry) getMiddleware(name, version string) (FactoryMethod, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	middlewareFn, ok := p.middleware[nameLower+"/"+versionLower]
	if ok {
		return p.applyLogger(middlewareFn), true
	}
	if components.IsInitialVersion(versionLower) {
		middlewareFn, ok = p.middleware[nameLower]
		if ok {
			return p.applyLogger(middlewareFn), true
		}
	}
	return nil, false
}

func (p *Registry) applyLogger(componentFactory func(logger.Logger) FactoryMethod) FactoryMethod {
	return componentFactory(p.Logger)
}

func createFullName(name string) string {
	return strings.ToLower("middleware.http." + name)
}
