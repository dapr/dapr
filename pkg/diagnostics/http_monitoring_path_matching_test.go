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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opencensus.io/stats/view"
)

func TestHTTPMetricsPathMatchingActorEndpoints(t *testing.T) {
	testHTTP := newHTTPMetrics()
	testHTTP.enabled = false
	// Normal configuration without double slashes
	paths := []string{
		"/v1.0/actors/{actorType}/{actorId}/method/{method}",
		"/v1.0/actors/{actorType}/{actorId}/reminders/{reminder}",
		"/v1.0/actors/{actorType}/{actorId}/timers/{timer}",
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

	// Test various actor endpoint patterns
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
			expectedMatch: "/v1.0/actors/{actorType}/{actorId}/reminders/{reminder}",
		},
		{
			name:          "actor timer",
			path:          "/v1.0/actors/WorkerActor/ActorID/timers/MyTimer",
			expectedMatch: "/v1.0/actors/{actorType}/{actorId}/timers/{timer}",
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

func TestHTTPMetricsPathMatchingUnmatchedPathWithSubtreeStrictMode(t *testing.T) {
	testHTTP := newHTTPMetrics()
	testHTTP.enabled = false

	// Only "/" is registered - should fallback to "/" for low cardinality
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
	require.True(t, ok)
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
