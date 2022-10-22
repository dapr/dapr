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

import (
	"net/http"
)

type Middleware = func(next http.Handler) http.Handler

// HTTPPipeline defines the middleware pipeline to be plugged into Dapr sidecar.
type Pipeline struct {
	Handlers []Middleware
}

func (p Pipeline) Apply(handler http.Handler) http.Handler {
	for i := len(p.Handlers) - 1; i >= 0; i-- {
		handler = p.Handlers[i](handler)
	}
	return handler
}
