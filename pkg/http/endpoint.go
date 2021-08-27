// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import "github.com/valyala/fasthttp"

// Endpoint is a collection of route information for an Dapr API. Either a combination of Route and Version or an Alias
// is required, and when Alias is present, it indicates extra necessary infos are provided via HTTP headers instead of
// via URL path.
type Endpoint struct {
	Methods []string
	Route   string
	Version string
	Alias   string
	Handler fasthttp.RequestHandler
}
