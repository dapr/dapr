// Copyright 2016 Qiang Xue. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package routing

import (
	"fmt"
	"net/url"
	"strings"
)

// Route represents a URL path pattern that can be used to match requested URLs.
type Route struct {
	group      *RouteGroup
	name, path string
	template   string
}

// newRoute creates a new Route with the given route path and route group.
func newRoute(path string, group *RouteGroup) *Route {
	path = group.prefix + path
	name := path

	// an asterisk at the end matches any number of characters
	if strings.HasSuffix(path, "*") {
		path = path[:len(path)-1] + "<:.*>"
	}

	route := &Route{
		group:    group,
		name:     name,
		path:     path,
		template: buildURLTemplate(path),
	}
	group.router.routes[name] = route

	return route
}

// Name sets the name of the route.
// This method will update the registration of the route in the router as well.
func (r *Route) Name(name string) *Route {
	r.name = name
	r.group.router.routes[name] = r
	return r
}

// Get adds the route to the router using the GET HTTP method.
func (r *Route) Get(handlers ...Handler) *Route {
	return r.add("GET", handlers)
}

// Post adds the route to the router using the POST HTTP method.
func (r *Route) Post(handlers ...Handler) *Route {
	return r.add("POST", handlers)
}

// Put adds the route to the router using the PUT HTTP method.
func (r *Route) Put(handlers ...Handler) *Route {
	return r.add("PUT", handlers)
}

// Patch adds the route to the router using the PATCH HTTP method.
func (r *Route) Patch(handlers ...Handler) *Route {
	return r.add("PATCH", handlers)
}

// Delete adds the route to the router using the DELETE HTTP method.
func (r *Route) Delete(handlers ...Handler) *Route {
	return r.add("DELETE", handlers)
}

// Connect adds the route to the router using the CONNECT HTTP method.
func (r *Route) Connect(handlers ...Handler) *Route {
	return r.add("CONNECT", handlers)
}

// Head adds the route to the router using the HEAD HTTP method.
func (r *Route) Head(handlers ...Handler) *Route {
	return r.add("HEAD", handlers)
}

// Options adds the route to the router using the OPTIONS HTTP method.
func (r *Route) Options(handlers ...Handler) *Route {
	return r.add("OPTIONS", handlers)
}

// Trace adds the route to the router using the TRACE HTTP method.
func (r *Route) Trace(handlers ...Handler) *Route {
	return r.add("TRACE", handlers)
}

// To adds the route to the router with the given HTTP methods and handlers.
// Multiple HTTP methods should be separated by commas (without any surrounding spaces).
func (r *Route) To(methods string, handlers ...Handler) *Route {
	for _, method := range strings.Split(methods, ",") {
		r.add(method, handlers)
	}
	return r
}

// URL creates a URL using the current route and the given parameters.
// The parameters should be given in the sequence of name1, value1, name2, value2, and so on.
// If a parameter in the route is not provided a value, the parameter token will remain in the resulting URL.
// The method will perform URL encoding for all given parameter values.
func (r *Route) URL(pairs ...interface{}) (s string) {
	s = r.template
	for i := 0; i < len(pairs); i++ {
		name := fmt.Sprintf("<%v>", pairs[i])
		value := ""
		if i < len(pairs)-1 {
			value = url.QueryEscape(fmt.Sprint(pairs[i+1]))
		}
		s = strings.Replace(s, name, value, -1)
	}
	return
}

// add registers the route, the specified HTTP method and the handlers to the router.
// The handlers will be combined with the handlers of the route group.
func (r *Route) add(method string, handlers []Handler) *Route {
	hh := combineHandlers(r.group.handlers, handlers)
	r.group.router.add(method, r.path, hh)
	return r
}

// buildURLTemplate converts a route pattern into a URL template by removing regular expressions in parameter tokens.
func buildURLTemplate(path string) string {
	template, start, end := "", -1, -1
	for i := 0; i < len(path); i++ {
		if path[i] == '<' && start < 0 {
			start = i
		} else if path[i] == '>' && start >= 0 {
			name := path[start+1 : i]
			for j := start + 1; j < i; j++ {
				if path[j] == ':' {
					name = path[start+1 : j]
					break
				}
			}
			template += path[end+1:start] + "<" + name + ">"
			end = i
			start = -1
		}
	}
	if end < 0 {
		template = path
	} else if end < len(path)-1 {
		template += path[end+1:]
	}
	return template
}

// combineHandlers merges two lists of handlers into a new list.
func combineHandlers(h1 []Handler, h2 []Handler) []Handler {
	hh := make([]Handler, len(h1)+len(h2))
	copy(hh, h1)
	copy(hh[len(h1):], h2)
	return hh
}
