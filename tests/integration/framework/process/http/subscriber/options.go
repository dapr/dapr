/*
Copyright 2024 The Dapr Authors
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

package subscriber

import (
	"net/http"

	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
)

type options struct {
	routes       []string
	bulkRoutes   []string
	handlerFuncs []app.Option
}

func WithRoutes(routes ...string) Option {
	return func(o *options) {
		o.routes = append(o.routes, routes...)
	}
}

func WithBulkRoutes(routes ...string) Option {
	return func(o *options) {
		o.bulkRoutes = append(o.bulkRoutes, routes...)
	}
}

func WithHandlerFunc(path string, fn http.HandlerFunc) Option {
	return func(o *options) {
		o.handlerFuncs = append(o.handlerFuncs, app.WithHandlerFunc(path, fn))
	}
}
