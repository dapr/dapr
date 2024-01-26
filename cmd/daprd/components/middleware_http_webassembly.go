//go:build allcomponents

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

package components

import (
	"context"

	contribmiddleware "github.com/dapr/components-contrib/middleware"
	"github.com/dapr/components-contrib/middleware/http/wasm"
	"github.com/dapr/dapr/pkg/components"
	httpMiddlewareLoader "github.com/dapr/dapr/pkg/components/middleware/http"
	"github.com/dapr/dapr/pkg/middleware"
	"github.com/dapr/kit/logger"
)

func init() {
	httpMiddlewareLoader.DefaultRegistry.RegisterComponent(func(log logger.Logger) httpMiddlewareLoader.FactoryMethod {
		return func(metadata contribmiddleware.Metadata) (middleware.HTTP, error) {
			return wasm.NewMiddleware(log).GetHandler(context.TODO(), metadata)
		}
	}, "wasm")
	components.RegisterWasmComponentType(components.CategoryMiddleware, "wasm")
}
