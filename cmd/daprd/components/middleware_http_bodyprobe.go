//go:build middleware_http_bodyprobe

/*
Copyright 2026 The Dapr Authors
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
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"testing/iotest"

	contribmiddleware "github.com/dapr/components-contrib/middleware"
	httpMiddlewareLoader "github.com/dapr/dapr/pkg/components/middleware/http"
	"github.com/dapr/dapr/pkg/middleware"
	"github.com/dapr/kit/logger"
)

// bodyProbe is an integration-test-only HTTP middleware that reads the entire
// request body, appends a record of how many bytes were readable (and whether
// the read failed) to the file named by the "path" metadata property, then
// restores the body so downstream handlers behave as usual. If the read
// failed, the restored body replays the bytes that were read and then returns
// the same error, so the downstream handler fails exactly as it would have
// without the probe. It lets tests assert whether the request body was already
// capped by the server's max body size limit by the time the middleware
// pipeline runs.
//
// It is gated behind the middleware_http_bodyprobe build tag, set only for
// the integration-test daprd binary and never for a released flavor, so it is
// never shipped.
func init() {
	httpMiddlewareLoader.DefaultRegistry.RegisterComponent(func(logger.Logger) httpMiddlewareLoader.FactoryMethod {
		return func(metadata contribmiddleware.Metadata) (middleware.HTTP, error) {
			path := metadata.Properties["path"]
			if path == "" {
				return nil, errors.New(`missing metadata property "path"`)
			}

			var mu sync.Mutex
			return func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, readErr := io.ReadAll(r.Body)

					mu.Lock()
					f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
					if err == nil {
						_, _ = fmt.Fprintf(f, "%s %s %d %t\n", r.Method, r.URL.EscapedPath(), len(body), readErr != nil)
						_ = f.Close()
					}
					mu.Unlock()

					var restored io.Reader = bytes.NewReader(body)
					if readErr != nil {
						restored = io.MultiReader(restored, iotest.ErrReader(readErr))
					}
					r.Body = io.NopCloser(restored)
					next.ServeHTTP(w, r)
				})
			}, nil
		}
	}, "bodyprobe")
}
