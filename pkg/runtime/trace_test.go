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

package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// A tracer obtained from the forwarding provider must follow the SDK
// provider swapped in by a runtime restart, and record nothing once no SDK
// provider is registered.
func TestForwardingTracerProvider(t *testing.T) {
	newProvider := func() (*sdktrace.TracerProvider, *tracetest.InMemoryExporter) {
		exp := tracetest.NewInMemoryExporter()
		return sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp)), exp
	}

	var fwd forwardingTracerProvider
	tracer := fwd.Tracer("test")

	tp1, exp1 := newProvider()
	fwd.current.Store(tp1)
	_, span := tracer.Start(t.Context(), "one")
	span.End()
	require.Len(t, exp1.GetSpans(), 1)

	// A restart shuts the old provider down before registering the new one.
	require.NoError(t, tp1.Shutdown(t.Context()))
	tp2, exp2 := newProvider()
	fwd.current.Store(tp2)
	_, span = tracer.Start(t.Context(), "two")
	span.End()
	require.Len(t, exp2.GetSpans(), 1)
	assert.Equal(t, "two", exp2.GetSpans()[0].Name)

	fwd.current.Store(nil)
	_, span = tracer.Start(t.Context(), "three")
	assert.False(t, span.IsRecording())
	span.End()
	assert.Len(t, exp2.GetSpans(), 1)
	require.NoError(t, tp2.Shutdown(t.Context()))
}
