// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"fmt"
	"sync"

	middleware "github.com/dapr/components-contrib/middleware"
	http_middleware "github.com/dapr/dapr/pkg/middleware/http"
)

// Registry is the interface for callers to get registered HTTP middleware
type Registry interface {
	CreateMiddleware(name string, metadata middleware.Metadata) (http_middleware.Middleware, error)
}

type httpMiddlewareRegistry struct {
	middleware map[string]func(middleware.Metadata) http_middleware.Middleware
}

var instance *httpMiddlewareRegistry
var once sync.Once

// NewRegistry returns a new HTTP middleware registry
func NewRegistry() Registry {
	once.Do(func() {
		instance = &httpMiddlewareRegistry{
			middleware: map[string]func(middleware.Metadata) http_middleware.Middleware{},
		}
	})
	return instance
}

// RegisterMiddleware registers a new HTTP middleware
func RegisterMiddleware(name string, factoryMethod func(metadata middleware.Metadata) http_middleware.Middleware) {
	instance.middleware[createFullName(name)] = factoryMethod
}

func createFullName(name string) string {
	return fmt.Sprintf("middleware.http.%s", name)
}

func (p *httpMiddlewareRegistry) CreateMiddleware(name string, metadata middleware.Metadata) (http_middleware.Middleware, error) {
	if method, ok := p.middleware[name]; ok {
		return method(metadata), nil
	}
	return nil, fmt.Errorf("couldn't find HTTP middleware %s", name)
}
