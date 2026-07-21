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

package http

import (
	"context"
	nethttp "net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(optionstimeout))
}

// optionstimeout verifies that --binding-options-timeout controls when daprd
// gives up waiting for an HTTP app to respond to the OPTIONS subscription
// discovery probe.
//
// Scenario:
//   - Both apps delay their OPTIONS response by 2 s to simulate a slow-starting
//     JVM/JIT workload.
//   - The default timeout is 3 s.
//     This test exercises the failure case by configuring daprdFail with 1 s and
//     the success case with 10 s.
//     daprdFail uses a 1 s timeout — shorter than the 2 s app delay — so its
//     OPTIONS probe times out and the binding is NOT activated.
//   - daprdCustom uses a 10 s timeout: well beyond the 2 s app delay, so its
//     OPTIONS probe always succeeds and the binding IS activated.
//
// Each daprd instance gets its own dedicated app so binding POST events are
// counted independently. The HTTP channel does NOT set dapr-app-id on outbound
// binding event POSTs, so header-based routing would not work.
type optionstimeout struct {
	// failApp is the app paired with daprdFail (1 s configured timeout < 2 s delay).
	failApp *app.App
	// customApp is the app paired with daprdCustom (10 s configured timeout > 2 s delay).
	customApp *app.App

	// daprdFail uses a 1 s OPTIONS probe timeout (shorter than the app's 2 s delay).
	daprdFail *daprd.Daprd
	// daprdCustom uses a 10 s OPTIONS probe timeout (longer than the app's 2 s delay).
	daprdCustom *daprd.Daprd

	// bindingCalledFail counts binding events delivered to failApp.
	bindingCalledFail atomic.Int64
	// bindingCalledCustom counts binding events delivered to customApp.
	bindingCalledCustom atomic.Int64
}

func (o *optionstimeout) Setup(t *testing.T) []framework.Option {
	const (
		// optionsDelay is the time the slow app takes to respond to the OPTIONS probe,
		// simulating a slow-starting JVM/JIT workload.
		optionsDelay = 2 * time.Second

		// failTimeout is shorter than optionsDelay so the probe times out and the
		// binding is NOT activated.
		failTimeout = optionsDelay / 2 // 1 s

		// customTimeout is much longer than optionsDelay so the probe always succeeds
		// and the binding IS activated.
		customTimeout = 5 * optionsDelay // 10 s
	)

	// A cron input binding that fires every second. The direction is intentionally
	// omitted so that daprd performs the OPTIONS probe rather than bypassing it.
	const bindingResource = `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mybinding
spec:
  type: bindings.cron
  version: v1
  metadata:
  - name: schedule
    value: "@every 1s"
`

	// Each daprd gets its own app instance so binding POST events are counted
	// independently — no header-based routing required.
	o.failApp = app.New(t,
		app.WithHandlerFunc("/mybinding", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			switch r.Method {
			case nethttp.MethodOptions:
				// Simulate a slow-starting application that exceeds the configured timeout.
				time.Sleep(optionsDelay)
				w.WriteHeader(nethttp.StatusOK)
			case nethttp.MethodPost:
				o.bindingCalledFail.Add(1)
				w.WriteHeader(nethttp.StatusOK)
			default:
				w.WriteHeader(nethttp.StatusMethodNotAllowed)
			}
		}),
	)

	o.customApp = app.New(t,
		app.WithHandlerFunc("/mybinding", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			switch r.Method {
			case nethttp.MethodOptions:
				// Same delay — but daprdCustom's 10 s timeout gives it room to succeed.
				time.Sleep(optionsDelay)
				w.WriteHeader(nethttp.StatusOK)
			case nethttp.MethodPost:
				o.bindingCalledCustom.Add(1)
				w.WriteHeader(nethttp.StatusOK)
			default:
				w.WriteHeader(nethttp.StatusMethodNotAllowed)
			}
		}),
	)

	// daprdFail: failTimeout < optionsDelay, so the OPTIONS probe times out.
	// StartReadingFromBindings returns an error logged as a warning; daprd
	// continues running but the binding is not activated.
	o.daprdFail = daprd.New(t,
		daprd.WithAppPort(o.failApp.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithBindingOptionsTimeout(failTimeout),
		daprd.WithResourceFiles(bindingResource),
	)

	// daprdCustom: customTimeout >> optionsDelay, so the OPTIONS probe succeeds.
	// StartReadingFromBindings activates the cron binding, which fires every second.
	o.daprdCustom = daprd.New(t,
		daprd.WithAppPort(o.customApp.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithBindingOptionsTimeout(customTimeout),
		daprd.WithResourceFiles(bindingResource),
	)

	return []framework.Option{
		framework.WithProcesses(o.failApp, o.customApp, o.daprdFail, o.daprdCustom),
	}
}

func (o *optionstimeout) Run(t *testing.T, ctx context.Context) {
	// StartReadingFromBindings is called synchronously during daprd startup, so
	// WaitUntilRunning returns only after the OPTIONS probe has either timed out
	// (daprdFail, ~1 s) or succeeded (daprdCustom, ~2 s). At this point the
	// cron binding is already active for daprdCustom.
	o.daprdFail.WaitUntilRunning(t, ctx)
	o.daprdCustom.WaitUntilRunning(t, ctx)

	gclientFail := o.daprdFail.GRPCClient(t, ctx)
	gclientCustom := o.daprdCustom.GRPCClient(t, ctx)

	// Both daprd instances must have registered the cron component even if the
	// probe failed — the component is initialized, just not actively reading.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := gclientFail.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		assert.NoError(c, err)
		assert.Len(c, resp.GetRegisteredComponents(), 1)
	}, time.Second*5, 10*time.Millisecond)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := gclientCustom.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		assert.NoError(c, err)
		assert.Len(c, resp.GetRegisteredComponents(), 1)
	}, time.Second*5, 10*time.Millisecond)

	// daprdCustom's OPTIONS probe succeeded (10 s > 2 s app delay),
	// so the binding SHOULD have fired at least once.
	require.Eventually(t, func() bool {
		return o.bindingCalledCustom.Load() > 0
	}, 5*time.Second, 100*time.Millisecond, "10 s timeout: binding should fire because OPTIONS probe succeeded")

	// daprdFail's OPTIONS probe timed out (1 s < 2 s app delay),
	// so the binding should NOT have been activated.
	assert.Equal(t, int64(0), o.bindingCalledFail.Load(),
		"1 s timeout: binding should NOT fire because OPTIONS probe timed out")
}
