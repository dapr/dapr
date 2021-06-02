// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import "github.com/valyala/fasthttp"

// Endpoint is a collection of route information for an Dapr API.
type Endpoint struct {
	Methods []string
	Route   string
	Version string
	Handler fasthttp.RequestHandler
}
