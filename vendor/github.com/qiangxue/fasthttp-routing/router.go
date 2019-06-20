// Copyright 2016 Qiang Xue. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package routing provides high performance and powerful HTTP routing capabilities.
package routing

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/valyala/fasthttp"
)

type (
	// Handler is the function for handling HTTP requests.
	Handler func(*Context) error

	// Router manages routes and dispatches HTTP requests to the handlers of the matching routes.
	Router struct {
		RouteGroup
		pool             sync.Pool
		routes           map[string]*Route
		stores           map[string]routeStore
		maxParams        int
		notFound         []Handler
		notFoundHandlers []Handler
	}

	// routeStore stores route paths and the corresponding handlers.
	routeStore interface {
		Add(key string, data interface{}) int
		Get(key string, pvalues []string) (data interface{}, pnames []string)
		String() string
	}
)

// Methods lists all supported HTTP methods by Router.
var Methods = []string{
	"CONNECT",
	"DELETE",
	"GET",
	"HEAD",
	"OPTIONS",
	"PATCH",
	"POST",
	"PUT",
	"TRACE",
}

// New creates a new Router object.
func New() *Router {
	r := &Router{
		routes: make(map[string]*Route),
		stores: make(map[string]routeStore),
	}
	r.RouteGroup = *newRouteGroup("", r, make([]Handler, 0))
	r.NotFound(MethodNotAllowedHandler, NotFoundHandler)
	r.pool.New = func() interface{} {
		return &Context{
			pvalues: make([]string, r.maxParams),
			router:  r,
		}
	}
	return r
}

// HandleRequest handles the HTTP request.
func (r *Router) HandleRequest(ctx *fasthttp.RequestCtx) {
	c := r.pool.Get().(*Context)
	c.init(ctx)
	c.handlers, c.pnames = r.find(string(ctx.Method()), string(ctx.Path()), c.pvalues)
	if err := c.Next(); err != nil {
		r.handleError(c, err)
	}
	r.pool.Put(c)
}

// Route returns the named route.
// Nil is returned if the named route cannot be found.
func (r *Router) Route(name string) *Route {
	return r.routes[name]
}

// Use appends the specified handlers to the router and shares them with all routes.
func (r *Router) Use(handlers ...Handler) {
	r.RouteGroup.Use(handlers...)
	r.notFoundHandlers = combineHandlers(r.handlers, r.notFound)
}

// NotFound specifies the handlers that should be invoked when the router cannot find any route matching a request.
// Note that the handlers registered via Use will be invoked first in this case.
func (r *Router) NotFound(handlers ...Handler) {
	r.notFound = handlers
	r.notFoundHandlers = combineHandlers(r.handlers, r.notFound)
}

// handleError is the error handler for handling any unhandled errors.
func (r *Router) handleError(c *Context, err error) {
	if httpError, ok := err.(HTTPError); ok {
		c.Error(httpError.Error(), httpError.StatusCode())
	} else {
		c.Error(err.Error(), http.StatusInternalServerError)
	}
}

func (r *Router) add(method, path string, handlers []Handler) {
	store := r.stores[method]
	if store == nil {
		store = newStore()
		r.stores[method] = store
	}
	if n := store.Add(path, handlers); n > r.maxParams {
		r.maxParams = n
	}
}

func (r *Router) find(method, path string, pvalues []string) (handlers []Handler, pnames []string) {
	var hh interface{}
	if store := r.stores[method]; store != nil {
		hh, pnames = store.Get(path, pvalues)
	}
	if hh != nil {
		return hh.([]Handler), pnames
	}
	return r.notFoundHandlers, pnames
}

func (r *Router) findAllowedMethods(path string) map[string]bool {
	methods := make(map[string]bool)
	pvalues := make([]string, r.maxParams)
	for m, store := range r.stores {
		if handlers, _ := store.Get(path, pvalues); handlers != nil {
			methods[m] = true
		}
	}
	return methods
}

// NotFoundHandler returns a 404 HTTP error indicating a request has no matching route.
func NotFoundHandler(*Context) error {
	return NewHTTPError(http.StatusNotFound)
}

// MethodNotAllowedHandler handles the situation when a request has matching route without matching HTTP method.
// In this case, the handler will respond with an Allow HTTP header listing the allowed HTTP methods.
// Otherwise, the handler will do nothing and let the next handler (usually a NotFoundHandler) to handle the problem.
func MethodNotAllowedHandler(c *Context) error {
	methods := c.Router().findAllowedMethods(string(c.Path()))
	if len(methods) == 0 {
		return nil
	}
	methods["OPTIONS"] = true
	ms := make([]string, len(methods))
	i := 0
	for method := range methods {
		ms[i] = method
		i++
	}
	sort.Strings(ms)
	c.Response.Header.Set("Allow", strings.Join(ms, ", "))
	if string(c.Method()) != "OPTIONS" {
		c.Response.SetStatusCode(http.StatusMethodNotAllowed)
	}
	c.Abort()
	return nil
}
