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

package state

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc/metadata"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
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
	suite.Register(new(state))
}

// state verifies that calls into a state store emit a client span that is a
// child of the daprd span for the request, for the HTTP and gRPC state APIs
// and for actor state.
type state struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *scheduler.Scheduler
	collector *otel.Collector
}

func (s *state) Setup(t *testing.T) []framework.Option {
	s.collector = otel.New(t)
	s.scheduler = scheduler.New(t)
	s.place = placement.New(t)

	srv := app.New(t,
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
`, s.collector.OTLPGRPCAddress())

	s.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.scheduler.Address()),
		daprd.WithAppPort(srv.Port()),
		daprd.WithConfigManifests(t, tracingConfig),
	)

	return []framework.Option{
		framework.WithProcesses(s.collector, s.scheduler, s.place, srv, s.daprd),
	}
}

func (s *state) Run(t *testing.T, ctx context.Context) {
	s.collector.WaitUntilRunning(t, ctx)
	s.scheduler.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	httpClient := fclient.HTTP(t)
	httpPost := func(t *testing.T, path, traceID, body string) int {
		t.Helper()
		url := fmt.Sprintf("http://%s/v1.0/%s", s.daprd.HTTPAddress(), path)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("traceparent", "00-"+traceID+"-0000000000000001-01")
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode
	}
	grpcCtx := func(traceID string) context.Context {
		return metadata.AppendToOutgoingContext(ctx, "traceparent", "00-"+traceID+"-0000000000000001-01")
	}

	t.Run("http save", func(t *testing.T) {
		const traceID = "00000000000000000000000000000011"
		code := httpPost(t, "state/mystore", traceID, `[{"key":"customer-key","value":"v"}]`)
		require.Equal(t, http.StatusNoContent, code)
		s.assertComponentSpan(t, traceID, "set mystore", 0)
	})

	t.Run("grpc get", func(t *testing.T) {
		const traceID = "00000000000000000000000000000022"
		_, err := s.daprd.GRPCClient(t, ctx).GetState(grpcCtx(traceID), &rtv1.GetStateRequest{
			StoreName: "mystore",
			Key:       "customer-key",
		})
		require.NoError(t, err)
		s.assertComponentSpan(t, traceID, "get mystore", 0)
	})

	t.Run("http transaction", func(t *testing.T) {
		const traceID = "00000000000000000000000000000033"
		code := httpPost(t, "state/mystore/transaction", traceID, `{"operations":[
			{"operation":"upsert","request":{"key":"customer-key","value":"v2"}},
			{"operation":"delete","request":{"key":"other-key"}}
		]}`)
		require.Equal(t, http.StatusNoContent, code)
		s.assertComponentSpan(t, traceID, "transaction mystore", 2)
	})

	t.Run("grpc bulk get", func(t *testing.T) {
		const traceID = "00000000000000000000000000000044"
		_, err := s.daprd.GRPCClient(t, ctx).GetBulkState(grpcCtx(traceID), &rtv1.GetBulkStateRequest{
			StoreName: "mystore",
			Keys:      []string{"customer-key", "other-key"},
		})
		require.NoError(t, err)
		s.assertComponentSpan(t, traceID, "bulk_get mystore", 2)
	})

	t.Run("actor state transaction", func(t *testing.T) {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, http.StatusOK, httpPost(t, "actors/myactortype/myactorid/method/foo", "00000000000000000000000000000055", ""))
		}, 10*time.Second, 10*time.Millisecond, "actor not ready in time")

		const traceID = "00000000000000000000000000000066"
		code := httpPost(t, "actors/myactortype/myactorid/state", traceID,
			`[{"operation":"upsert","request":{"key":"customer-key","value":"v"}}]`)
		require.Equal(t, http.StatusNoContent, code)
		s.assertComponentSpan(t, traceID, "transaction mystore", 0)
	})
}

// assertComponentSpan waits for a client span named name in the trace and
// checks that its parent is a daprd span in the same trace, that it carries
// the component attributes, and that no span name in the trace contains the
// state key. batchSize is the expected db.operation.batch.size, 0 for none.
func (s *state) assertComponentSpan(t *testing.T, traceID, name string, batchSize int64) {
	t.Helper()

	wantTrace, err := hex.DecodeString(traceID)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		spans := spansInTrace(s.collector.GetSpans(), wantTrace)
		var comp *tracepb.Span
		ids := make(map[string]struct{}, len(spans))
		for _, sp := range spans {
			ids[string(sp.GetSpanId())] = struct{}{}
			assert.NotContains(c, sp.GetName(), "customer-key")
			if sp.GetName() == name {
				comp = sp
			}
		}
		if !assert.NotNil(c, comp, "no %q span", name) {
			return
		}
		assert.Equal(c, tracepb.Span_SPAN_KIND_CLIENT, comp.GetKind())
		_, ok := ids[string(comp.GetParentSpanId())]
		assert.True(c, ok, "parent of %q is not a daprd span", name)

		attrs := make(map[string]*commonv1.AnyValue)
		for _, kv := range comp.GetAttributes() {
			attrs[kv.GetKey()] = kv.GetValue()
		}
		assert.Equal(c, "in-memory", attrs["db.system.name"].GetStringValue())
		assert.Equal(c, "mystore", attrs["dapr.component.name"].GetStringValue())
		assert.Equal(c, "state.in-memory", attrs["dapr.component.type"].GetStringValue())
		if batchSize > 0 {
			assert.Equal(c, batchSize, attrs["db.operation.batch.size"].GetIntValue())
		} else {
			assert.NotContains(c, attrs, "db.operation.batch.size")
		}
	}, 20*time.Second, 100*time.Millisecond)
}

func spansInTrace(rss []*tracepb.ResourceSpans, traceID []byte) []*tracepb.Span {
	var res []*tracepb.Span
	for _, rs := range rss {
		for _, ss := range rs.GetScopeSpans() {
			for _, sp := range ss.GetSpans() {
				if bytes.Equal(sp.GetTraceId(), traceID) {
					res = append(res, sp)
				}
			}
		}
	}
	return res
}
