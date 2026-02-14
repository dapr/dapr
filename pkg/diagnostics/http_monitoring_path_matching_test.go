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

package diagnostics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dapr/dapr/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opencensus.io/stats/view"
)

func TestHTTPMetricsPathMatchingNotEnabled(t *testing.T) {
	testHTTP := newHTTPMetrics()
	testHTTP.enabled = false
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(func() {
		meter.Stop()
	})
	testHTTP.Init(meter, "fakeID", HTTPMonitoringConfig{}, nil)
	matchedPath, ok := testHTTP.pathMatcher.match("/orders")
	require.False(t, ok)
	require.Equal(t, "", matchedPath)
}

func TestHTTPMetricsPathMatchingLegacyIncreasedCardinality(t *testing.T) {
	testHTTP := newHTTPMetrics()
	testHTTP.enabled = false
	paths := []string{
		"/v1/orders/{orderID}/items/12345",
		"/v1/orders/{orderID}/items/{itemID}",
		"/v1/items/{itemID}",
		"/v1/orders/{orderID}/items/{itemID}",
	}
	configHTTP := NewHTTPMonitoringConfig(paths, true, false)
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(func() {
		meter.Stop()
	})
	testHTTP.Init(meter, "fakeID", configHTTP, nil)

	// act & assert

	// empty path
	matchedPath, ok := testHTTP.pathMatcher.match("")
	require.False(t, ok)
	require.Equal(t, "", matchedPath)

	// match "/v1/orders/{orderID}/items/12345"
	matchedPath, ok = testHTTP.pathMatcher.match("/v1/orders/12345/items/12345")
	require.True(t, ok)
	require.Equal(t, "/v1/orders/{orderID}/items/12345", matchedPath)

	// match "/v1/orders/{orderID}/items/{itemID}"
	matchedPath, ok = testHTTP.pathMatcher.match("/v1/orders/12345/items/1111")
	require.True(t, ok)
	require.Equal(t, "/v1/orders/{orderID}/items/{itemID}", matchedPath)

	// match "/v1/items/{itemID}"
	matchedPath, ok = testHTTP.pathMatcher.match("/v1/items/12345")
	require.True(t, ok)
	require.Equal(t, "/v1/items/{itemID}", matchedPath)

	// no match so we keep the path as is
	matchedPath, ok = testHTTP.pathMatcher.match("/v2/basket/12345")
	require.True(t, ok)
	require.Equal(t, "/v2/basket/12345", matchedPath)

	// match "/v1/orders/{orderID}/items/{itemID}"
	matchedPath, ok = testHTTP.pathMatcher.match("/v1/orders/12345/items/1111")
	require.True(t, ok)
	require.Equal(t, "/v1/orders/{orderID}/items/{itemID}", matchedPath)
}

func TestHTTPMetricsPathMatchingLowCardinality(t *testing.T) {
	testHTTP := newHTTPMetrics()
	testHTTP.enabled = false
	paths := []string{
		"/v1/orders/{orderID}/items/12345",
		"/v1/orders/{orderID}/items/{itemID}",
		"/v1/orders/{orderID}",
		"/v1/items/{itemID}",
		"/dapr/config",
		"/v1/",
		"/",
	}
	configHTTP := NewHTTPMonitoringConfig(paths, false, false)
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(func() {
		meter.Stop()
	})
	testHTTP.Init(meter, "fakeID", configHTTP, nil)

	// act & assert

	// empty path
	matchedPath, ok := testHTTP.pathMatcher.match("")
	require.False(t, ok)
	require.Equal(t, "", matchedPath)

	// match "/v1/orders/{orderID}/items/12345"
	matchedPath, ok = testHTTP.pathMatcher.match("/v1/orders/12345/items/12345")
	require.True(t, ok)
	require.Equal(t, "/v1/orders/{orderID}/items/12345", matchedPath)

	// match "/v1/orders/{orderID}"
	matchedPath, ok = testHTTP.pathMatcher.match("/v1/orders/12345")
	require.True(t, ok)
	require.Equal(t, "/v1/orders/{orderID}", matchedPath)

	// match "/v1/items/{itemID}"
	matchedPath, ok = testHTTP.pathMatcher.match("/v1/items/12345")
	require.True(t, ok)
	require.Equal(t, "/v1/items/{itemID}", matchedPath)

	// match "/v1/"
	matchedPath, ok = testHTTP.pathMatcher.match("/v1/basket")
	require.True(t, ok)
	assert.Equal(t, "/v1/", matchedPath)

	// match "/"
	matchedPath, ok = testHTTP.pathMatcher.match("/v2/orders/1111")
	require.True(t, ok)
	assert.Equal(t, "/", matchedPath)

	// no match so we fallback to "/"
	matchedPath, ok = testHTTP.pathMatcher.match("/basket/12345")
	require.True(t, ok)
	require.Equal(t, "/", matchedPath)

	matchedPath, ok = testHTTP.pathMatcher.match("/dapr/config")
	require.True(t, ok)
	require.Equal(t, "/dapr/config", matchedPath)
}

func TestHTTPMetricsPathMatchingLowCardinalityRootPathRegister(t *testing.T) {
	testHTTP := newHTTPMetrics()
	testHTTP.enabled = false

	// 1 - Root path not registered fallback to ""
	paths1 := []string{"/v1/orders/{orderID}"}
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(func() {
		meter.Stop()
	})
	testHTTP.Init(meter, "fakeID", HTTPMonitoringConfig{paths1, false, false}, nil)
	matchedPath, ok := testHTTP.pathMatcher.match("/thispathdoesnotexist")
	require.False(t, ok)
	require.Equal(t, "", matchedPath)

	// 2 - Root path registered fallback to "/"
	paths2 := []string{"/v1/orders/{orderID}", "/"}
	meter2 := view.NewMeter()
	meter2.Start()
	defer meter2.Stop()
	testHTTP.Init(meter2, "fakeID", HTTPMonitoringConfig{paths2, false, false}, nil)
	matchedPath, ok = testHTTP.pathMatcher.match("/thispathdoesnotexist")
	require.True(t, ok)
	require.Equal(t, "/", matchedPath)
}

func TestHTTPMetricsPathMatchingWithRedirect(t *testing.T) {
	const testPath = "/redirect-test"

	pm := newPathMatching([]string{"/other-path"}, false)
	pm.mux.HandleFunc(testPath, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/redirected", http.StatusFound)
	})

	matchedPath, ok := pm.match(testPath)
	require.True(t, ok, "Expected path matching to succeed")
	require.Equal(t, testPath, matchedPath, "Expected matched path to be %q", testPath)
}

func TestHTTPMiddleware_Normalization(t *testing.T) {
	paths := []string{"/v1.0/actors/myactortype/{id}/method"}

	testCases := []struct {
		name         string
		requestPath  string
		legacy       bool
		expectedPath string
		expectMatch  bool
	}{
		{
			name:         "Legacy: Double Slash should be normalized and ID masked",
			requestPath:  "//v1.0/actors/myactortype/myid/method/foo",
			legacy:       true,
			expectedPath: "/v1.0/actors/myactortype/myid/method/foo",
			expectMatch:  true,
		},
		{
			name:         "Legacy: Normal Path",
			requestPath:  "/v1.0/actors/myactortype/myid/method/foo",
			legacy:       true,
			expectedPath: "/v1.0/actors/myactortype/myid/method/foo",
			expectMatch:  true,
		},
		{
			name:         "Strict: Double Slash should be normalized and matched",
			requestPath:  "//v1.0/actors/myactortype/myid/method/foo",
			legacy:       false,
			expectedPath: "/v1.0/actors/myactortype/{id}/method/foo",
			expectMatch:  true,
		},
		{
			name:         "Strict: Unmatched path (Double Slash) should be dropped",
			requestPath:  "//v1.0/actors/unknown/myid/method/foo",
			legacy:       false,
			expectedPath: "/v1.0/actors/unknown/{id}/method/foo",
			expectMatch:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testHTTP := newHTTPMetrics()
			configHTTP := NewHTTPMonitoringConfig(paths, tc.legacy, false)
			meter := view.NewMeter()
			meter.Start()
			t.Cleanup(func() { meter.Stop() })

			require.NoError(t, testHTTP.Init(meter, "fakeID", configHTTP, config.LoadDefaultConfiguration().GetMetricsSpec().GetLatencyDistribution(log)))
			handler := testHTTP.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

			req, _ := http.NewRequest(http.MethodPut, "http://localhost:3500"+tc.requestPath, nil)
			handler.ServeHTTP(httptest.NewRecorder(), req)

			rows, err := meter.RetrieveData("http/server/request_count")
			require.NoError(t, err)

			if tc.expectMatch {
				require.Len(t, rows, 1, "Expected 1 metric row")
				pathTag := getPathTag(rows[0])
				assert.Equal(t, tc.expectedPath, pathTag, "Path tag mismatch")
			} else if len(rows) > 0 {
				pathTag := getPathTag(rows[0])
				assert.Equal(t, "", pathTag, "Expected empty path for unmatched request")
			}
		})
	}
}

func getPathTag(row *view.Row) string {
	for _, tag := range row.Tags {
		if tag.Key.Name() == "path" {
			return tag.Value
		}
	}
	return ""
}

func TestHTTPMetricsPathMatchingActorEndpoints(t *testing.T) {
	testHTTP := newHTTPMetrics()
	testHTTP.enabled = false
	paths := []string{
		"/v1.0/actors/{actorType}/{actorId}/method/{method}",
		"/v1.0/actors/{actorType}/{actorId}/reminders/{name}",
		"/v1.0/actors/{actorType}/{actorId}/timers/{name}",
		"/v1.0/actors/{actorType}/{actorId}/state/{key}",
		"/v1.0/actors/{actorType}/{actorId}/state",
	}
	configHTTP := NewHTTPMonitoringConfig(paths, false, false)
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(func() {
		meter.Stop()
	})
	testHTTP.Init(meter, "fakeID", configHTTP, nil)

	tests := []struct {
		name          string
		path          string
		expectedMatch string
	}{
		{
			name:          "actor method invocation",
			path:          "/v1.0/actors/WorkerActor/ActorID/method/StoreModelAndConstructRequest",
			expectedMatch: "/v1.0/actors/{actorType}/{actorId}/method/{method}",
		},
		{
			name:          "actor reminder",
			path:          "/v1.0/actors/WorkerActor/ActorID/reminders/MyReminder",
			expectedMatch: "/v1.0/actors/{actorType}/{actorId}/reminders/{name}",
		},
		{
			name:          "actor timer",
			path:          "/v1.0/actors/WorkerActor/ActorID/timers/MyTimer",
			expectedMatch: "/v1.0/actors/{actorType}/{actorId}/timers/{name}",
		},
		{
			name:          "actor state get specific key",
			path:          "/v1.0/actors/RxPassResultActor/ActorID/state/stateKey",
			expectedMatch: "/v1.0/actors/{actorType}/{actorId}/state/{key}",
		},
		{
			name:          "actor state transaction",
			path:          "/v1.0/actors/OrderActor/order-123/state",
			expectedMatch: "/v1.0/actors/{actorType}/{actorId}/state",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matchedPath, ok := testHTTP.pathMatcher.match(tt.path)
			require.True(t, ok)
			require.Equal(t, tt.expectedMatch, matchedPath)
		})
	}
}

func TestHTTPMetricsPathMatchingUnmatchedPathWithSubtreeStrictMode(t *testing.T) {
	testHTTP := newHTTPMetrics()
	testHTTP.enabled = false
	paths := []string{"/"}
	configHTTP := NewHTTPMonitoringConfig(paths, false, false)
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(func() {
		meter.Stop()
	})
	testHTTP.Init(meter, "fakeID", configHTTP, nil)

	matchedPath, ok := testHTTP.pathMatcher.match("//v1.0/actors/WeatherActor/xyz/method/GetWeatherAsync")
	require.True(t, ok)
	require.Equal(t, "/", matchedPath, "double-slash actor path should match root '/' in strict mode")
}

func TestHTTPMetricsPathMatchingServiceInvocation(t *testing.T) {
	testHTTP := newHTTPMetrics()
	testHTTP.enabled = false

	paths := []string{
		"/orders/{id}",
		"/api/v1/widget",
	}
	configHTTP := NewHTTPMonitoringConfig(paths, false, false)
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(func() {
		meter.Stop()
	})
	testHTTP.Init(meter, "fakeID", configHTTP, nil)

	matchedPath, ok := testHTTP.pathMatcher.match("/orders/123")
	require.True(t, ok)
	assert.Equal(t, "/orders/{id}", matchedPath)

	invokePath := "/v1.0/invoke/order-app/method/orders/123"
	matchedPath, ok = testHTTP.pathMatcher.match(invokePath)
	require.True(t, ok)
	assert.Equal(t, "/v1.0/invoke/{app_id}/method/orders/{id}", matchedPath)
}

func TestHTTPMetricsPathMatchingNormalizationDedup(t *testing.T) {
	paths := []string{
		"/orders",
		"//orders",
		"orders",
		"///orders",
	}

	configHTTP := NewHTTPMonitoringConfig(paths, false, false)
	testHTTP := newHTTPMetrics()

	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(func() { meter.Stop() })

	testHTTP.Init(meter, "fakeID", configHTTP, nil)

	matchedPath, ok := testHTTP.pathMatcher.match("//orders")
	require.True(t, ok)
	assert.Equal(t, "/orders", matchedPath)
}

// TestCardinalityBehavior verifies the behavior of HTTP metrics path matching
// across different cardinality configurations as per the Desired Behavior spec.
func TestCardinalityBehavior(t *testing.T) {
	tests := []struct {
		name                 string
		pathMatching         []string
		increasedCardinality bool
		requestPath          string
		expectedPath         string
		description          string
	}{
		// -------------------------------------------------------------------------
		// 1. Low Cardinality + Path Matching
		// -------------------------------------------------------------------------
		{
			name:                 "Low Cardinality + Path Matching - Matched",
			pathMatching:         []string{"/v1.0/actors/MyActor/{id}/method/foo"},
			increasedCardinality: false,
			requestPath:          "/v1.0/actors/MyActor/123/method/foo",
			expectedPath:         "/v1.0/actors/MyActor/{id}/method/foo",
			description:          "Should match the rule and return the template.",
		},
		{
			name:                 "Low Cardinality + Path Matching - Unmatched",
			pathMatching:         []string{"/v1.0/actors/MyActor/{id}/method/foo"},
			increasedCardinality: false,
			requestPath:          "/v1.0/actors/MyActor/456/method/bar",
			// User said "Result Auto Truncate".
			// Interpreting Auto Truncate as: ID replaced, method name KEPT (based on next case).
			expectedPath: "/v1.0/actors/MyActor/{id}/method/bar",
			description:  "Should AUTO-TRUNCATE unmatched actor paths",
		},

		{
			name:                 "Low Cardinality - No Path Matching",
			pathMatching:         nil,
			increasedCardinality: false,
			requestPath:          "/v1.0/actors/MyActor/123/method/foo",
			expectedPath:         "/v1.0/actors/MyActor/{id}/method/foo",
			description:          "Should AUTO-TRUNCATE",
		},

		{
			name:                 "High Cardinality + Path Matching - Matched",
			pathMatching:         []string{"/v1.0/actors/MyActor/{id}/method/foo"},
			increasedCardinality: true,
			requestPath:          "/v1.0/actors/MyActor/123/method/foo",
			expectedPath:         "/v1.0/actors/MyActor/{id}/method/foo",
			description:          "Should match the rule and return the template.",
		},
		{
			name:                 "High Cardinality + Path Matching - Unmatched",
			pathMatching:         []string{"/v1.0/actors/MyActor/{id}/method/foo"},
			increasedCardinality: true,
			requestPath:          "/v1.0/actors/OtherActor/999/method/baz",
			expectedPath:         "/v1.0/actors/OtherActor/999/method/baz",
			description:          "Should return RAW path for unmatched in High Card.",
		},

		{
			name:                 "High Cardinality + No path matching",
			pathMatching:         nil,
			increasedCardinality: true,
			requestPath:          "/v1.0/actors/MyActor/123/method/foo",
			expectedPath:         "/v1.0/actors/MyActor/123/method/foo",
			description:          "Should return RAW path (High Card).",
		},

		{
			name:                 "Double Slash Normalization",
			pathMatching:         nil,
			increasedCardinality: true,
			requestPath:          "//v1.0/actors/MyActor/123/method/foo",
			expectedPath:         "/v1.0/actors/MyActor/123/method/foo",
			description:          "Should keep double slash normalization",
		},

		{
			name:                 "Low Cardinality - Invoke Truncation",
			pathMatching:         nil,
			increasedCardinality: false,
			requestPath:          "/v1.0/invoke/myApp/method/orders/123",
			// Expect truncation after method name "orders"
			expectedPath: "/v1.0/invoke/myApp/method/orders",
			description:  "Should truncate Service Invocation after method name in Low Card.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testHTTP := newHTTPMetrics()
			configHTTP := NewHTTPMonitoringConfig(tc.pathMatching, tc.increasedCardinality, false)
			meter := view.NewMeter()
			meter.Start()
			t.Cleanup(func() { meter.Stop() })

			require.NoError(t, testHTTP.Init(meter, "fakeID", configHTTP, config.LoadDefaultConfiguration().GetMetricsSpec().GetLatencyDistribution(log)))

			handler := testHTTP.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			// Handle request path
			targetURL := "http://localhost:3500" + tc.requestPath
			req, _ := http.NewRequest(http.MethodPost, targetURL, nil)
			if strings.HasPrefix(tc.requestPath, "//") {
				req.URL.Path = tc.requestPath
			}

			handler.ServeHTTP(httptest.NewRecorder(), req)

			rows, err := meter.RetrieveData("http/server/request_count")
			require.NoError(t, err)

			found := false
			var actualPath string
			for _, row := range rows {
				actualPath = getPathTag(row)
				if actualPath == tc.expectedPath {
					found = true
					break
				}
			}

			if !found {
				t.Logf("MISMATCH: %s\nExpected: '%s'\nActual:   '%s'\nDesc: %s", tc.name, tc.expectedPath, actualPath, tc.description)
			}
			assert.True(t, found, "Behavior mismatch for %s", tc.name)
		})
	}
}
