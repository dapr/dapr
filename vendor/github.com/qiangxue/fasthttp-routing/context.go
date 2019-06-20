// Copyright 2016 Qiang Xue. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package routing

import (
	"fmt"

	"github.com/valyala/fasthttp"
)

// SerializeFunc serializes the given data of arbitrary type into a byte array.
type SerializeFunc func(data interface{}) ([]byte, error)

// Context represents the contextual data and environment while processing an incoming HTTP request.
type Context struct {
	*fasthttp.RequestCtx

	Serialize SerializeFunc // the function serializing the given data of arbitrary type into a byte array.

	router   *Router
	pnames   []string               // list of route parameter names
	pvalues  []string               // list of parameter values corresponding to pnames
	data     map[string]interface{} // data items managed by Get and Set
	index    int                    // the index of the currently executing handler in handlers
	handlers []Handler              // the handlers associated with the current route
}

// Router returns the Router that is handling the incoming HTTP request.
func (c *Context) Router() *Router {
	return c.router
}

// Param returns the named parameter value that is found in the URL path matching the current route.
// If the named parameter cannot be found, an empty string will be returned.
func (c *Context) Param(name string) string {
	for i, n := range c.pnames {
		if n == name {
			return c.pvalues[i]
		}
	}
	return ""
}

// Get returns the named data item previously registered with the context by calling Set.
// If the named data item cannot be found, nil will be returned.
func (c *Context) Get(name string) interface{} {
	return c.data[name]
}

// Set stores the named data item in the context so that it can be retrieved later.
func (c *Context) Set(name string, value interface{}) {
	if c.data == nil {
		c.data = make(map[string]interface{})
	}
	c.data[name] = value
}

// Next calls the rest of the handlers associated with the current route.
// If any of these handlers returns an error, Next will return the error and skip the following handlers.
// Next is normally used when a handler needs to do some postprocessing after the rest of the handlers
// are executed.
func (c *Context) Next() error {
	c.index++
	for n := len(c.handlers); c.index < n; c.index++ {
		if err := c.handlers[c.index](c); err != nil {
			return err
		}
	}
	return nil
}

// Abort skips the rest of the handlers associated with the current route.
// Abort is normally used when a handler handles the request normally and wants to skip the rest of the handlers.
// If a handler wants to indicate an error condition, it should simply return the error without calling Abort.
func (c *Context) Abort() {
	c.index = len(c.handlers)
}

// URL creates a URL using the named route and the parameter values.
// The parameters should be given in the sequence of name1, value1, name2, value2, and so on.
// If a parameter in the route is not provided a value, the parameter token will remain in the resulting URL.
// Parameter values will be properly URL encoded.
// The method returns an empty string if the URL creation fails.
func (c *Context) URL(route string, pairs ...interface{}) string {
	if r := c.router.routes[route]; r != nil {
		return r.URL(pairs...)
	}
	return ""
}

// WriteData writes the given data of arbitrary type to the response.
// The method calls the Serialize() method to convert the data into a byte array and then writes
// the byte array to the response.
func (c *Context) WriteData(data interface{}) (err error) {
	var bytes []byte
	if bytes, err = c.Serialize(data); err == nil {
		_, err = c.Write(bytes)
	}
	return
}

// init sets the request and response of the context and resets all other properties.
func (c *Context) init(ctx *fasthttp.RequestCtx) {
	c.RequestCtx = ctx
	c.data = nil
	c.index = -1
	c.Serialize = Serialize
}

// Serialize converts the given data into a byte array.
// If the data is neither a byte array nor a string, it will call fmt.Sprint to convert it into a string.
func Serialize(data interface{}) (bytes []byte, err error) {
	switch data.(type) {
	case []byte:
		return data.([]byte), nil
	case string:
		return []byte(data.(string)), nil
	default:
		if data != nil {
			return []byte(fmt.Sprint(data)), nil
		}
	}
	return nil, nil
}
