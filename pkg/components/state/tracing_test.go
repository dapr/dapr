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

package state_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	contribstate "github.com/dapr/components-contrib/state"
	compstate "github.com/dapr/dapr/pkg/components/state"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	daprt "github.com/dapr/dapr/pkg/testing"
)

var (
	recorderOnce sync.Once
	recorder     *tracetest.SpanRecorder
	testTracer   trace.Tracer
)

// spanRecorder installs a global tracer provider once per test binary: the
// diagnostics package tracer is bound to the first provider set.
func spanRecorder(t *testing.T) (*tracetest.SpanRecorder, trace.Tracer) {
	t.Helper()
	recorderOnce.Do(func() {
		recorder = tracetest.NewSpanRecorder()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
		otel.SetTracerProvider(tp)
		testTracer = tp.Tracer("test")
	})
	return recorder, testTracer
}

// childSpans returns the ended spans whose parent is parent.
func childSpans(rec *tracetest.SpanRecorder, parent trace.SpanContext) []sdktrace.ReadOnlySpan {
	var res []sdktrace.ReadOnlySpan
	for _, s := range rec.Ended() {
		if s.Parent().SpanID() == parent.SpanID() && s.Parent().TraceID() == parent.TraceID() {
			res = append(res, s)
		}
	}
	return res
}

func attrs(s sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	m := make(map[attribute.Key]attribute.Value, len(s.Attributes()))
	for _, kv := range s.Attributes() {
		m[kv.Key] = kv.Value
	}
	return m
}

type failingStore struct {
	*daprt.FakeStateStore
}

func (failingStore) Get(context.Context, *contribstate.GetRequest) (*contribstate.GetResponse, error) {
	return nil, errors.New("boom")
}

func TestWithTracing(t *testing.T) {
	rec, tr := spanRecorder(t)

	newStore := func() contribstate.Store {
		return compstate.WithTracing(daprt.NewFakeStateStore(), "statestore", "state.mongodb")
	}

	t.Run("get under an OpenTelemetry parent span", func(t *testing.T) {
		ctx, parent := tr.Start(t.Context(), "dapr.proto.runtime.v1.Dapr/GetState")
		_, err := newStore().Get(ctx, &contribstate.GetRequest{Key: "secret-customer-key"})
		require.NoError(t, err)
		parent.End()

		spans := childSpans(rec, parent.SpanContext())
		require.Len(t, spans, 1)
		s := spans[0]
		assert.Equal(t, "get statestore", s.Name())
		assert.Equal(t, trace.SpanKindClient, s.SpanKind())
		assert.Equal(t, otelcodes.Unset, s.Status().Code)

		a := attrs(s)
		assert.Equal(t, "mongodb", a["db.system.name"].AsString())
		assert.Equal(t, "get", a["db.operation.name"].AsString())
		assert.Equal(t, "statestore", a["dapr.component.name"].AsString())
		assert.Equal(t, "state.mongodb", a["dapr.component.type"].AsString())
		for k, v := range a {
			assert.NotContains(t, v.String(), "secret-customer-key", "key leaked into attribute %s", k)
		}
	})

	t.Run("get under an HTTP API parent span", func(t *testing.T) {
		// The HTTP API middleware stores its span under a Dapr context key, not
		// the OpenTelemetry one.
		_, parent := tr.Start(t.Context(), "/v1.0/state/statestore/key")
		r := httptest.NewRequest(http.MethodGet, "/v1.0/state/statestore/key", nil)
		diagUtils.AddSpanToRequest(r, parent)
		require.False(t, trace.SpanFromContext(r.Context()).SpanContext().IsValid())

		_, err := newStore().Get(r.Context(), &contribstate.GetRequest{Key: "key"})
		require.NoError(t, err)
		parent.End()

		spans := childSpans(rec, parent.SpanContext())
		require.Len(t, spans, 1)
		assert.Equal(t, parent.SpanContext().TraceID(), spans[0].SpanContext().TraceID())
	})

	t.Run("no span without a parent", func(t *testing.T) {
		before := len(rec.Ended())
		store := newStore()
		require.NoError(t, store.Set(t.Context(), &contribstate.SetRequest{Key: "k", Value: "v"}))
		res, err := store.Get(t.Context(), &contribstate.GetRequest{Key: "k"})
		require.NoError(t, err)
		assert.NotNil(t, res)
		assert.Len(t, rec.Ended(), before)
	})

	t.Run("no span under an unsampled parent", func(t *testing.T) {
		before := len(rec.Ended())
		sc := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: trace.TraceID{1},
			SpanID:  trace.SpanID{1},
		})
		ctx := trace.ContextWithSpanContext(t.Context(), sc)
		_, err := newStore().Get(ctx, &contribstate.GetRequest{Key: "k"})
		require.NoError(t, err)
		assert.Len(t, rec.Ended(), before)
	})

	t.Run("error sets status and error.type", func(t *testing.T) {
		store := compstate.WithTracing(failingStore{daprt.NewFakeStateStore()}, "statestore", "state.redis")
		ctx, parent := tr.Start(t.Context(), "parent")
		_, err := store.Get(ctx, &contribstate.GetRequest{Key: "k"})
		require.Error(t, err)
		parent.End()

		spans := childSpans(rec, parent.SpanContext())
		require.Len(t, spans, 1)
		assert.Equal(t, otelcodes.Error, spans[0].Status().Code)
		assert.Equal(t, "boom", spans[0].Status().Description)
		assert.Equal(t, "*errors.errorString", attrs(spans[0])["error.type"].AsString())
	})

	t.Run("bulk and transaction record batch size", func(t *testing.T) {
		store := newStore()
		ctx, parent := tr.Start(t.Context(), "parent")
		require.NoError(t, store.BulkSet(ctx, []contribstate.SetRequest{
			{Key: "a", Value: "1"}, {Key: "b", Value: "2"}, {Key: "c", Value: "3"},
		}, contribstate.BulkStoreOpts{}))
		_, err := store.BulkGet(ctx, []contribstate.GetRequest{{Key: "a"}}, contribstate.BulkGetOpts{})
		require.NoError(t, err)
		tx, ok := compstate.As[contribstate.TransactionalStore](store)
		require.True(t, ok)
		require.NoError(t, tx.Multi(ctx, &contribstate.TransactionalStateRequest{
			Operations: []contribstate.TransactionalStateOperation{
				contribstate.SetRequest{Key: "d", Value: "4"},
				contribstate.DeleteRequest{Key: "a"},
			},
		}))
		parent.End()

		spans := childSpans(rec, parent.SpanContext())
		require.Len(t, spans, 3)

		byName := map[string]sdktrace.ReadOnlySpan{}
		for _, s := range spans {
			byName[s.Name()] = s
		}
		assert.Equal(t, int64(3), attrs(byName["bulk_set statestore"])["db.operation.batch.size"].AsInt64())
		_, hasBatch := attrs(byName["bulk_get statestore"])["db.operation.batch.size"]
		assert.False(t, hasBatch, "a batch of one is not a batch")
		assert.Equal(t, int64(2), attrs(byName["transaction statestore"])["db.operation.batch.size"].AsInt64())
	})

	t.Run("one span per attempt", func(t *testing.T) {
		store := newStore()
		ctx, parent := tr.Start(t.Context(), "parent")
		for range 3 {
			_, err := store.Get(ctx, &contribstate.GetRequest{Key: "k"})
			require.NoError(t, err)
		}
		parent.End()
		assert.Len(t, childSpans(rec, parent.SpanContext()), 3)
	})
}

func TestAs(t *testing.T) {
	inner := daprt.NewFakeStateStore()
	wrapped := compstate.WithTracing(inner, "statestore", "state.in-memory")

	// The wrapper implements every optional interface, which is why callers
	// must not use a plain type assertion.
	_, ok := wrapped.(contribstate.Querier)
	require.True(t, ok)

	for name, store := range map[string]contribstate.Store{"raw": inner, "wrapped": wrapped} {
		t.Run(name, func(t *testing.T) {
			_, ok := compstate.As[contribstate.TransactionalStore](store)
			assert.True(t, ok)
			_, ok = compstate.As[contribstate.TransactionalStoreMultiMaxSize](store)
			assert.True(t, ok)
			_, ok = compstate.As[contribstate.Querier](store)
			assert.False(t, ok)
			_, ok = compstate.As[contribstate.KeysLiker](store)
			assert.False(t, ok)
			_, ok = compstate.As[contribstate.DeleteWithPrefix](store)
			assert.False(t, ok)
		})
	}

	t.Run("returns the traced implementation", func(t *testing.T) {
		tx, ok := compstate.As[contribstate.TransactionalStore](wrapped)
		require.True(t, ok)
		assert.NotSame(t, inner, tx)
		assert.Same(t, inner, wrapped.(compstate.Unwrapper).Unwrap())
	})

	t.Run("ping is not reported for a store without it", func(t *testing.T) {
		assert.ErrorIs(t, contribstate.Ping(t.Context(), wrapped), contribstate.ErrPingNotImplemented)
	})
}
