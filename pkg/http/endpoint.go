/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package http

import "github.com/valyala/fasthttp"

// Endpoint is a collection of route information for an Dapr API.
//
// If an Alias, e.g. "hello", is provided along with the Route, e.g. "invoke/app-id/method/hello" and the Version,
// "v1.0", then two endpoints will be installed instead of one. Besiding the canonical Dapr API URL
// "/v1.0/invoke/app-id/method/hello", one another URL "/hello" is provided for the Alias. When Alias URL is used,
// extra infos are required to pass through HTTP headers, for example, application's ID.
type Endpoint struct {
	Methods           []string
	Route             string
	Version           string
	Alias             string
	KeepParamUnescape bool // keep the param in path unescaped
	Handler           fasthttp.RequestHandler
	AlwaysAllowed     bool // Endpoint is always allowed regardless of API access rules
}
