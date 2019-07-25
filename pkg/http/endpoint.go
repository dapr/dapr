package http

import routing "github.com/qiangxue/fasthttp-routing"

// Endpoint is a collection of route information for an Actions API
type Endpoint struct {
	Methods []string
	Route   string
	Version string
	Handler func(c *routing.Context) error
}
