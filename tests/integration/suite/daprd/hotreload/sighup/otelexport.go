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

package sighup

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/log"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/otel"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(otelexport))
}

// otelexport ensures that spans are still exported after a SIGHUP restart
// replaces the runtime and its tracer provider.
type otelexport struct {
	daprd     *daprd.Daprd
	collector *otel.Collector
	log       *log.Log
}

func (o *otelexport) Setup(t *testing.T) []framework.Option {
	o.log = log.New()
	o.collector = otel.New(t)

	testApp := app.New(t,
		app.WithHandlerFunc("/hi", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "OK")
		}),
	)

	o.daprd = daprd.New(t,
		daprd.WithAppPort(testApp.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppID("testapp"),
		o.collector.GRPCDaprdConfiguration(t),
		daprd.WithExecOptions(exec.WithStdout(o.log), exec.WithStderr(o.log)),
	)

	return []framework.Option{
		framework.WithProcesses(o.collector, testApp, o.daprd),
	}
}

func (o *otelexport) Run(t *testing.T, ctx context.Context) {
	o.collector.WaitUntilRunning(t, ctx)
	o.daprd.WaitUntilRunning(t, ctx)

	invoke := func(t *testing.T, traceID string) {
		t.Helper()
		tid, err := trace.TraceIDFromHex(traceID)
		require.NoError(t, err)
		o.daprd.HTTPPost2xx(t, ctx, "/v1.0/invoke/testapp/method/hi", nil,
			"traceparent", "00-"+traceID+"-00f067aa0ba902b7-01",
		)
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.NotEmpty(c, o.collector.TraceSpans(tid))
		}, 20*time.Second, 10*time.Millisecond)
	}

	t.Run("spans exported before SIGHUP", func(t *testing.T) {
		invoke(t, "4bf92f3577b34da6a3ce929d0e0e4736")
	})

	o.log.Reset()
	o.daprd.SignalHUP(t)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, o.log.Contains("dapr initialized. Status: Running"))
	}, 10*time.Second, 10*time.Millisecond)
	o.daprd.WaitUntilRunning(t, ctx)

	t.Run("spans exported after SIGHUP", func(t *testing.T) {
		invoke(t, "5bf92f3577b34da6a3ce929d0e0e4737")
	})
}
