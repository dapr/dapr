package http

import routing "github.com/qiangxue/fasthttp-routing"

type Endpoint struct {
	Methods []string
	Route   string
	Version string
	Handler func(c *routing.Context) error
}
