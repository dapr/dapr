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

package diagnostics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opencensus.io/stats/view"
)

// The patterns come from spec.metrics.http.pathMatching in the Configuration.
// http.ServeMux panics on a pattern it cannot parse and on a pattern that
// overlaps an earlier one. A bad entry must be ignored instead of crashing
// daprd, and the other entries must keep working.
func TestHTTPMetricsPathMatchingInvalidPatterns(t *testing.T) {
	tests := map[string]struct {
		paths    []string
		path     string
		expected string
	}{
		"unterminated wildcard is ignored": {
			paths:    []string{"/orders/{id", "/items"},
			path:     "/items",
			expected: "/items",
		},
		"duplicate wildcard name is ignored": {
			paths:    []string{"/orders/{id}/items/{id}", "/items"},
			path:     "/items",
			expected: "/items",
		},
		"same path with another wildcard name is ignored": {
			paths:    []string{"/orders/{id}", "/orders/{orderID}"},
			path:     "/orders/5",
			expected: "/orders/{id}",
		},
		"overlapping path is ignored": {
			paths:    []string{"/orders/{id}", "/{resource}/list"},
			path:     "/orders/5",
			expected: "/orders/{id}",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			testHTTP := newHTTPMetrics()
			meter := view.NewMeter()
			meter.Start()
			t.Cleanup(meter.Stop)

			require.NotPanics(t, func() {
				require.NoError(t, testHTTP.Init(meter, "fakeID", NewHTTPMonitoringConfig(test.paths, false, false), view.Distribution(1, 10, 100)))
			})

			matchedPath, ok := testHTTP.pathMatcher.match(test.path)
			require.True(t, ok)
			assert.Equal(t, test.expected, matchedPath)
		})
	}
}
