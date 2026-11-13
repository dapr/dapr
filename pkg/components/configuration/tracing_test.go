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

package configuration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	c "github.com/dapr/components-contrib/configuration"
	"github.com/dapr/dapr/pkg/components/configuration"
)

type fakeConfigStore struct {
	c.Store
	subscribeCtx context.Context
}

func (f *fakeConfigStore) Get(context.Context, *c.GetRequest) (*c.GetResponse, error) {
	return &c.GetResponse{}, nil
}

func (f *fakeConfigStore) Subscribe(ctx context.Context, _ *c.SubscribeRequest, _ c.UpdateHandler) (string, error) {
	f.subscribeCtx = ctx
	return "sub-id", nil
}

func (f *fakeConfigStore) Unsubscribe(context.Context, *c.UnsubscribeRequest) error {
	return nil
}

func TestWithTracing(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	otel.SetTracerProvider(tp)
	tr := tp.Tracer("test")

	inner := &fakeConfigStore{}
	store := configuration.WithTracing(inner, "configstore", "configuration.redis")

	ctx, parent := tr.Start(t.Context(), "parent")
	_, err := store.Get(ctx, &c.GetRequest{Keys: []string{"k"}})
	require.NoError(t, err)
	id, err := store.Subscribe(ctx, &c.SubscribeRequest{Keys: []string{"k"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, "sub-id", id)
	require.NoError(t, store.Unsubscribe(ctx, &c.UnsubscribeRequest{ID: id}))
	parent.End()

	// The long-lived subscription context must not carry the subscribe span,
	// which has already ended.
	assert.Equal(t, parent.SpanContext(), trace.SpanContextFromContext(inner.subscribeCtx))

	names := make([]string, 0, 3)
	for _, s := range rec.Ended() {
		if s.Parent().SpanID() != parent.SpanContext().SpanID() {
			continue
		}
		names = append(names, s.Name())
		assert.Equal(t, trace.SpanKindClient, s.SpanKind())
		for _, kv := range s.Attributes() {
			if kv.Key == "db.system.name" {
				assert.Equal(t, "redis", kv.Value.AsString())
			}
		}
	}
	assert.ElementsMatch(t, []string{
		"get configstore",
		"subscribe configstore",
		"unsubscribe configstore",
	}, names)
}
