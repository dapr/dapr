//go:build allcomponents || stablecomponents

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
	"net/http"
	"strings"

	contribmiddleware "github.com/dapr/components-contrib/middleware"
	httpMiddlewareLoader "github.com/dapr/dapr/pkg/components/middleware/http"
	"github.com/dapr/dapr/pkg/middleware"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
)

func init() {
	httpMiddlewareLoader.DefaultRegistry.RegisterComponent(func(log logger.Logger) httpMiddlewareLoader.FactoryMethod {
		return func(metadata contribmiddleware.Metadata) (middleware.HTTP, error) {
			// Apply to request only by default
			var request, response bool
			switch strings.ToLower(metadata.Properties["direction"]) {
			case "response":
				request = false
				response = true
			case "both":
				request = true
				response = true
			case "request":
				request = true
				response = false
			default:
				request = true
				response = false
			}

			if response && request {
				return func(next http.Handler) http.Handler {
					return utils.UppercaseRequestMiddleware(
						utils.UppercaseResponseMiddleware(next),
					)
				}, nil
			} else if response {
				return utils.UppercaseResponseMiddleware, nil
			} else if request {
				return utils.UppercaseRequestMiddleware, nil
			}

			// Should never get here
			return nil, nil
		}
	}, "uppercase")
}
