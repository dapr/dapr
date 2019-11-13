// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"fmt"
	"sync"

	http_middleware "github.com/dapr/dapr/pkg/middleware/http"
)

// Registry is the interface for callers to get registered exporter components
type Registry interface {
	CreateMiddleware(name string) (http_middleware.Middleware, error)
}

type httpMiddlewareRegistry struct {
	middleware map[string]func() http_middleware.Middleware
}

var instance *httpMiddlewareRegistry
var once sync.Once

// NewRegistry returns a new exporter registry
func NewRegistry() Registry {
	once.Do(func() {
		instance = &httpMiddlewareRegistry{
			middleware: map[string]func() http_middleware.Middleware{},
		}
	})
	return instance
}

// RegisterExporter registers a new exporter
func RegisterMiddleware(name string, factoryMethod func() http_middleware.Middleware) {
	instance.middleware[createFullName(name)] = factoryMethod
}

func createFullName(name string) string {
	return fmt.Sprintf("middleware.http.%s", name)
}

func (p *httpMiddlewareRegistry) CreateMiddleware(name string) (http_middleware.Middleware, error) {
	if method, ok := p.middleware[name]; ok {
		return method(), nil
	}
	return nil, fmt.Errorf("couldn't find HTTP middleware %s", name)
}
