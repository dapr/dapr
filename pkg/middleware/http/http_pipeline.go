// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"strings"

	"github.com/dapr/dapr/pkg/config"
	"github.com/valyala/fasthttp"
)

const (
	// Operators
	andOperator = "and"
	orOperator  = "or"

	// Keys
	pathKey     = "path"
	versionKey  = "version"
	methodKey   = "method"
	operatorKey = "operator"
)

// Middleware represents a middleware handler
type Middleware struct {
	Handler  func(h fasthttp.RequestHandler) fasthttp.RequestHandler
	Selector map[string][]string
}

func NewMiddleware(handler func(h fasthttp.RequestHandler) fasthttp.RequestHandler) *Middleware {
	return &Middleware{
		Handler: handler,
	}
}

// HTTPPipeline defines the middleware pipeline to be plugged into Dapr sidecar
type Pipeline struct {
	Handlers []*Middleware
}

func BuildHTTPPipeline(spec config.PipelineSpec) (Pipeline, error) {
	return Pipeline{}, nil
}

func matchSelector(path, version string, method string, selectors map[string][]string) bool {

	pathSelector, pathSelectorExists := selectors[pathKey]
	versionSelector, versionSelectorExists := selectors[versionKey]
	methodSelector, methodSelectorExists := selectors[methodKey]

	// No selector set
	if !pathSelectorExists && !versionSelectorExists && !methodSelectorExists {
		return true
	}

	// Atleast one selector set so must match criteria
	matchedPath := false
	if pathSelectorExists {
		for _, selectorPath := range pathSelector {
			if strings.HasPrefix(path, selectorPath) {
				matchedPath = true
				break
			}
		}
	}

	matchedVersion := false
	if versionSelectorExists {
		for _, selectorVersion := range versionSelector {
			if strings.EqualFold(version, selectorVersion) {
				matchedVersion = true
				break
			}
		}
	}

	matchedMethods := false
	if methodSelectorExists {
		for _, selectorMethod := range methodSelector {
			if strings.EqualFold(method, selectorMethod) {
				matchedMethods = true
				break
			}
		}
	}

	var operator string
	if selectorOperator, ok := selectors[operatorKey]; ok {
		operator = selectorOperator[0] // Only consider a single operator
	}
	switch strings.ToLower(operator) {
	case andOperator:
		return matchedPath && matchedMethods && matchedVersion
	case orOperator:
		return matchedPath || matchedMethods || matchedVersion
	default:
		return matchedPath || matchedMethods || matchedVersion
	}
}

// Apply creates the middleware pipeline for a specific endpoint
func (p Pipeline) Apply(path, version string, method string, handler fasthttp.RequestHandler) fasthttp.RequestHandler {
	for i := len(p.Handlers) - 1; i >= 0; i-- {
		mw := p.Handlers[i]
		if matchSelector(path, version, method, mw.Selector) {
			// Only add handler to pipeline if this endpoint matches the selectors
			handler = mw.Handler(handler)
		}
	}
	return handler
}
