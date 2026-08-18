/*
Copyright 2025 The Dapr Authors
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

package actors

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/otel"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(actors))
}

// actors verifies that the reminder and timer HTTP endpoints emit bounded
// trace span names. The raw request path embeds the unbounded actorId and
// reminder/timer name, so using it as the span name exploded tracing
// cardinality (one distinct span name per actor instance per reminder). The
// span name must keep only the bounded actorType.
//
// Regression test for https://github.com/dapr/dapr/issues/4703
type actors struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *scheduler.Scheduler
	collector *otel.Collector
}

func (a *actors) Setup(t *testing.T) []framework.Option {
	a.collector = otel.New(t)
	a.scheduler = scheduler.New(t)
	a.place = placement.New(t)

	srv := app.New(
		t,
		app.WithHandlerFunc("/actors/myactortype/myactorid", func(http.ResponseWriter, *http.Request) {}),
		app.WithHandlerFunc("/actors/myactortype/myactorid/method/foo", func(http.ResponseWriter, *http.Request) {}),
		app.WithConfig(`{"entities": ["myactortype"]}`),
	)

	tracingConfig := fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: tracing
spec:
  tracing:
    samplingRate: "1.0"
    otel:
      endpointAddress: %s
      protocol: grpc
      isSecure: false
`, a.collector.OTLPGRPCAddress())

	a.daprd = daprd.New(
		t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithSchedulerAddresses(a.scheduler.Address()),
		daprd.WithAppPort(srv.Port()),
		daprd.WithConfigManifests(t, tracingConfig),
	)

	return []framework.Option{
		framework.WithProcesses(a.collector, a.scheduler, a.place, srv, a.daprd),
	}
}

func (a *actors) Run(t *testing.T, ctx context.Context) {
	a.collector.WaitUntilRunning(t, ctx)
	a.scheduler.WaitUntilRunning(t, ctx)
	a.place.WaitUntilRunning(t, ctx)
	a.daprd.WaitUntilRunning(t, ctx)

	client := fclient.HTTP(t)
	baseURL := "http://localhost:" + strconv.Itoa(a.daprd.HTTPPort()) + "/v1.0/actors/myactortype/myactorid"

	// Wait for the actor to be hosted before registering reminders/timers.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/method/foo", nil)
		require.NoError(c, err)
		resp, err := client.Do(req)
		if assert.NoError(c, err) {
			assert.NoError(c, resp.Body.Close())
			assert.Equal(c, http.StatusOK, resp.StatusCode)
		}
	}, 10*time.Second, 10*time.Millisecond, "actor not ready in time")

	// Exercise every reminder/timer endpoint in endpointGroupActorV1Misc. A far
	// future dueTime keeps the reminder/timer from firing during the test. The
	// traceparent forces the span to be sampled (and thus named + exported).
	do := func(method, path, body string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, baseURL+path, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("traceparent", "00-00000000000000000000000000000001-0000000000000001-01")
		resp, err := client.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.GreaterOrEqual(t, resp.StatusCode, http.StatusOK)
		assert.Less(t, resp.StatusCode, http.StatusMultipleChoices)
	}

	do(http.MethodPost, "/reminders/myremindername", `{"dueTime": "10000s"}`)
	do(http.MethodGet, "/reminders/myremindername", "")
	do(http.MethodDelete, "/reminders/myremindername", "")
	do(http.MethodPost, "/timers/mytimername", `{"dueTime": "10000s"}`)
	do(http.MethodDelete, "/timers/mytimername", "")

	expected := []string{
		"RegisterActorReminder/myactortype",
		"GetActorReminder/myactortype",
		"UnregisterActorReminder/myactortype",
		"RegisterActorTimer/myactortype",
		"UnregisterActorTimer/myactortype",
	}

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		names := spanNames(a.collector.GetSpans())
		for _, exp := range expected {
			assert.Contains(c, names, exp)
		}
	}, 20*time.Second, 100*time.Millisecond, "did not receive bounded reminder/timer span names")

	// The unbounded actorId and reminder/timer name must never leak into a span
	// name (that is the high-cardinality regression this guards against).
	for _, name := range spanNames(a.collector.GetSpans()) {
		assert.NotContains(t, name, "myactorid", "span name must not contain the unbounded actorId")
		assert.NotContains(t, name, "myremindername", "span name must not contain the unbounded reminder name")
		assert.NotContains(t, name, "mytimername", "span name must not contain the unbounded timer name")
	}
}

func spanNames(spans []*tracepb.ResourceSpans) []string {
	var names []string
	for _, rs := range spans {
		for _, ss := range rs.GetScopeSpans() {
			for _, s := range ss.GetSpans() {
				names = append(names, s.GetName())
			}
		}
	}
	return names
}
