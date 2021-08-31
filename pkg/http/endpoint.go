// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import "github.com/valyala/fasthttp"

// Endpoint is a collection of route information for an Dapr API.
//
// If an Alias, e.g. "hello", is provided along with the Route, e.g. "invoke/app-id/method/hello" and the Version,
// "v1.0", then two endpoints will be installed instead of one. Besiding the canonical Dapr API URL
// "/v1.0/invoke/app-id/method/hello", one another URL "/hello" is provided for the Alias. When Alias URL is used,
// extra infos are required to pass through HTTP headers, for example, application's ID.
type Endpoint struct {
	Methods []string
	Route   string
	Version string
	Alias   string
	Handler fasthttp.RequestHandler
}
