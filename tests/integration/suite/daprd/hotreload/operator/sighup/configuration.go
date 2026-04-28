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

package sighup

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/log"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(configuration))
}

// configuration tests that a ConfigurationUpdate event from the operator
// triggers a SIGHUP restart of daprd, causing the new configuration to be
// loaded.
type configuration struct {
	daprd    *daprd.Daprd
	operator *operator.Operator
	logOut   *log.Log

	configSent  chan struct{}
	eventSent   atomic.Bool
	samplingCh  atomic.Value
	sentryAddr  string
	trustAnchor string
}

func (c *configuration) Setup(t *testing.T) []framework.Option {
	c.logOut = log.New()
	c.configSent = make(chan struct{})

	// Start with sampling rate 0
	c.samplingCh.Store("0")

	snt := sentry.New(t)
	c.sentryAddr = snt.Address()
	c.trustAnchor = string(snt.CABundle().X509.TrustAnchors)

	c.operator = operator.New(t,
		operator.WithSentry(snt),
		operator.WithGetConfigurationFn(func(_ context.Context, _ *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			rate := c.samplingCh.Load().(string)
			config := map[string]any{
				"kind":       "Configuration",
				"apiVersion": "dapr.io/v1alpha1",
				"metadata":   map[string]any{"name": "tracing"},
				"spec": map[string]any{
					"tracing": map[string]any{
						"samplingRate": rate,
					},
				},
			}
			b, err := json.Marshal(config)
			if err != nil {
				return nil, err
			}
			return &operatorv1.GetConfigurationResponse{Configuration: b}, nil
		}),
		operator.WithConfigurationUpdateFn(func(_ *operatorv1.ConfigurationUpdateRequest, srv operatorv1.Operator_ConfigurationUpdateServer) error {
			// Only send the event on the first stream connection.
			// After SIGHUP, daprd reconnects and we must not re-send
			// the event or it will trigger another SIGHUP loop.
			if !c.eventSent.Load() {
				select {
				case <-srv.Context().Done():
					return nil
				case <-c.configSent:
				}
				c.eventSent.Store(true)
				b, err := json.Marshal(map[string]any{
					"kind":       "Configuration",
					"apiVersion": "dapr.io/v1alpha1",
					"metadata":   map[string]any{"name": "tracing"},
					"spec": map[string]any{
						"tracing": map[string]any{
							"samplingRate": c.samplingCh.Load().(string),
						},
					},
				})
				if err != nil {
					return err
				}
				if err := srv.Send(&operatorv1.ConfigurationUpdateEvent{
					Configuration: b,
					Type:          operatorv1.ResourceEventType_UPDATED,
				}); err != nil {
					return err
				}
			}
			// Keep the stream open
			<-srv.Context().Done()
			return errors.New("stream closed")
		}),
	)

	c.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("tracing"),
		daprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", c.trustAnchor),
			exec.WithStdout(c.logOut),
			exec.WithStderr(c.logOut),
		),
		daprd.WithSentryAddress(c.sentryAddr),
		daprd.WithControlPlaneAddress(c.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
	)

	return []framework.Option{
		framework.WithProcesses(snt, c.operator, c.daprd),
	}
}

func (c *configuration) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)

	t.Run("initial configuration has sampling rate 0", func(t *testing.T) {
		assert.EventuallyWithT(t, func(ct *assert.CollectT) {
			assert.True(ct, c.logOut.Contains("TraceIDRatioBased{0}"),
				"expected log to show TraceIDRatioBased{0} for sampling rate 0")
		}, 5*time.Second, 10*time.Millisecond)
	})

	t.Run("configuration update via operator stream triggers SIGHUP reload", func(t *testing.T) {
		// Change the sampling rate that will be returned by GetConfiguration
		c.samplingCh.Store("1")

		c.logOut.Reset()

		// Send a ConfigurationUpdate event on the operator stream.
		// The SIGHUP reconciler will receive this and send SIGHUP to daprd.
		close(c.configSent)

		// After SIGHUP, daprd restarts and calls GetConfiguration again,
		// which now returns sampling rate 1.
		c.daprd.WaitUntilRunning(t, ctx)

		assert.EventuallyWithT(t, func(ct *assert.CollectT) {
			assert.True(ct, c.logOut.Contains("AlwaysOnSampler"),
				"expected log to show AlwaysOnSampler for sampling rate 1 after SIGHUP")
		}, 15*time.Second, 10*time.Millisecond)
	})
}
