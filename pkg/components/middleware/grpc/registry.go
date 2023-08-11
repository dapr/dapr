package grpc

import (
	"fmt"
	"github.com/dapr/components-contrib/middleware"
	"github.com/dapr/dapr/pkg/components"
	grpcMiddleware "github.com/dapr/dapr/pkg/middleware/grpc"
	"github.com/dapr/kit/logger"
	"strings"
)

type UnaryRegistry struct {
	Logger     logger.Logger
	middleware map[string]func(logger.Logger) FactoryMethod
}

// FactoryMethod is the method creating middleware from metadata.
type FactoryMethod func(metadata middleware.Metadata) (grpcMiddleware.Middleware, error)

// DefaultUnaryRegistry is the singleton with the registry.
var DefaultUnaryRegistry *UnaryRegistry

func init() {
	DefaultUnaryRegistry = NewUnaryRegistry()
}

// NewUnaryRegistry returns a new HTTP middleware registry.
func NewUnaryRegistry() *UnaryRegistry {
	return &UnaryRegistry{
		middleware: map[string]func(logger.Logger) FactoryMethod{},
	}
}

// RegisterComponent adds a new HTTP middleware to the registry.
func (p *UnaryRegistry) RegisterComponent(componentFactory func(logger.Logger) FactoryMethod, names ...string) {
	for _, name := range names {
		p.middleware[createFullName(name)] = componentFactory
	}
}

// Create instantiates a HTTP middleware based on `name`.
func (p *UnaryRegistry) Create(name, version string, metadata middleware.Metadata, logName string) (grpcMiddleware.Middleware, error) {
	if method, ok := p.getMiddleware(name, version, logName); ok {
		mid, err := method(metadata)
		if err != nil {
			return nil, fmt.Errorf("error creating gRPC Unary middleware %s/%s: %w", name, version, err)
		}
		return mid, nil
	}
	return nil, fmt.Errorf("gRPC Unary middleware %s/%s has not been registered", name, version)
}

func (p *UnaryRegistry) getMiddleware(name, version, logName string) (FactoryMethod, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	middlewareFn, ok := p.middleware[nameLower+"/"+versionLower]
	if ok {
		return p.applyLogger(middlewareFn, logName), true
	}
	if components.IsInitialVersion(versionLower) {
		middlewareFn, ok = p.middleware[nameLower]
		if ok {
			return p.applyLogger(middlewareFn, logName), true
		}
	}
	return nil, false
}

func (p *UnaryRegistry) applyLogger(componentFactory func(logger.Logger) FactoryMethod, logName string) FactoryMethod {
	l := p.Logger
	if logName != "" && l != nil {
		l = l.WithFields(map[string]any{
			"component": logName,
		})
	}
	return componentFactory(l)
}

func createFullName(name string) string {
	return strings.ToLower("middleware.grpc.unary." + name)
}
