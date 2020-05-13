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
	// Keys
	pathKey    = "path"
	versionKey = "version"
	methodKey  = "method"
)

// Middleware represents a middleware handler
type Middleware struct {
	Handler  func(h fasthttp.RequestHandler) fasthttp.RequestHandler
	Selector map[string]string
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

type selectorRule struct {
	Paths    []string
	Methods  []string
	Versions []string
}

func newSelectorRule(ruleSpec string) *selectorRule {
	// Expected rule spec schema:
	// path=path1,path2...;method=method1,method2...;version=version1,version2...;
	// Bad rules are ignored rather than error.
	rule := selectorRule{}
	parts := strings.Split(ruleSpec, ";")
	for _, part := range parts {
		exp := strings.Split(part, "=")
		if len(exp) != 2 {
			continue
		}
		lhs := exp[0]
		rhs := exp[1]

		if strings.EqualFold(lhs, pathKey) {
			// TODO: Sanitize paths
			paths := strings.ReplaceAll(rhs, " ", "")
			if len(paths) != 0 {
				ps := strings.Split(paths, ",")
				for _, p := range ps {
					if p != "" {
						rule.Paths = append(rule.Paths, p)
					}
				}
			}
		}
		if strings.EqualFold(lhs, methodKey) {
			// TODO: Sanitize method verbs
			methods := strings.ReplaceAll(rhs, " ", "")
			if len(methods) != 0 {
				ms := strings.Split(methods, ",")
				for _, m := range ms {
					if m != "" {
						rule.Methods = append(rule.Methods, m)
					}
				}
			}
		}
		if strings.EqualFold(lhs, versionKey) {
			// TODO: Sanitize versions
			versions := strings.ReplaceAll(rhs, " ", "")
			if len(versions) != 0 {
				vs := strings.Split(versions, ",")
				for _, v := range vs {
					if v != "" {
						rule.Versions = append(rule.Versions, v)
					}
				}
			}
		}
	}

	return &rule
}

func matchSelector(path, version string, method string, selector map[string]string) bool {
	// No selector, accept all
	if selector == nil {
		return true
	}

	var rules []*selectorRule
	for _, ruleSpec := range selector {
		r := newSelectorRule(ruleSpec)
		rules = append(rules, r)
	}

	// No selector rules set, accept all
	if len(rules) < 1 {
		return true
	}

	for _, rule := range rules {

		// Path prefix matching
		matchedPath := false
		if len(rule.Paths) < 1 {
			// If no path rule is defined, accept all paths
			matchedPath = true
		} else {
			// If atleast one path rule is defined then the
			// path must match atleast one of the rules
			for _, p := range rule.Paths {
				if strings.HasPrefix(path, p) {
					matchedPath = true
					break
				}
			}
		}

		// Methods matching
		matchedMethod := false
		if len(rule.Methods) < 1 {
			// If no method rule is defined, accept all methods
			matchedMethod = true
		} else {
			// If atleast one method rule is defined then the
			// method must match atleast one of the rules
			for _, m := range rule.Methods {
				if strings.EqualFold(method, m) {
					matchedMethod = true
					break
				}
			}
		}

		// Version matching
		matchedVersion := false
		if len(rule.Versions) < 1 {
			// If no version rule is defined, accept all versions
			matchedVersion = true
		} else {
			// If atleast one version rule is defined then the
			// version must match atleast one of the rules
			for _, v := range rule.Versions {
				if strings.EqualFold(version, v) {
					matchedVersion = true
					break
				}
			}
		}

		matchedRule := matchedPath && matchedMethod && matchedVersion
		if matchedRule {
			return true
		}
	}

	return false
}

// Apply creates the middleware pipeline for a specific endpoint
func (p Pipeline) Apply(path, version string, method string, handler fasthttp.RequestHandler) fasthttp.RequestHandler {
	for i := len(p.Handlers) - 1; i >= 0; i-- {
		mw := p.Handlers[i]
		if matchSelector(path, version, method, mw.Selector) {
			// Only add handler to pipeline if this endpoint matches the selector
			handler = mw.Handler(handler)
		}
	}
	return handler
}
