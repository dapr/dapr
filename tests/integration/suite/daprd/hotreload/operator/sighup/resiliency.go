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
	suite.Register(new(resiliency))
}

// resiliency tests that a ResiliencyUpdate event from the operator triggers a
// SIGHUP restart of daprd, causing the new resiliency policy to be loaded.
type resiliency struct {
	daprd    *daprd.Daprd
	operator *operator.Operator
	logOut   *log.Log

	resiliencySent chan struct{}
	eventSent      atomic.Bool
	sentryAddr     string
	trustAnchor    string
}

func (r *resiliency) Setup(t *testing.T) []framework.Option {
	r.logOut = log.New()
	r.resiliencySent = make(chan struct{})

	snt := sentry.New(t)
	r.sentryAddr = snt.Address()
	r.trustAnchor = string(snt.CABundle().X509.TrustAnchors)

	r.operator = operator.New(t,
		operator.WithSentry(snt),
		operator.WithGetConfigurationFn(func(_ context.Context, _ *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: []byte(
					`{"kind":"Configuration","apiVersion":"dapr.io/v1alpha1","metadata":{"name":"hotreloading"},"spec":{"features":[{"name":"HotReload","enabled":true}]}}`,
				),
			}, nil
		}),
		operator.WithListResiliencyFn(func(_ context.Context, _ *operatorv1.ListResiliencyRequest) (*operatorv1.ListResiliencyResponse, error) {
			return new(operatorv1.ListResiliencyResponse), nil
		}),
		operator.WithResiliencyUpdateFn(func(_ *operatorv1.ResiliencyUpdateRequest, srv operatorv1.Operator_ResiliencyUpdateServer) error {
			// Only send the event on the first stream connection.
			// After SIGHUP, daprd reconnects and we must not re-send
			// the event or it will trigger another SIGHUP loop.
			if !r.eventSent.Load() {
				select {
				case <-srv.Context().Done():
					return nil
				case <-r.resiliencySent:
				}
				r.eventSent.Store(true)
				b, err := json.Marshal(map[string]any{
					"kind":       "Resiliency",
					"apiVersion": "dapr.io/v1alpha1",
					"metadata":   map[string]any{"name": "myresiliency"},
					"spec": map[string]any{
						"policies": map[string]any{
							"timeouts": map[string]any{
								"general": "5s",
							},
						},
					},
				})
				if err != nil {
					return err
				}
				if err := srv.Send(&operatorv1.ResiliencyUpdateEvent{
					Resiliency: b,
					Type:       operatorv1.ResourceEventType_CREATED,
				}); err != nil {
					return err
				}
			}
			<-srv.Context().Done()
			return errors.New("stream closed")
		}),
	)

	r.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", r.trustAnchor),
			exec.WithStdout(r.logOut),
			exec.WithStderr(r.logOut),
		),
		daprd.WithSentryAddress(r.sentryAddr),
		daprd.WithControlPlaneAddress(r.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
	)

	return []framework.Option{
		framework.WithProcesses(snt, r.operator, r.daprd),
	}
}

func (r *resiliency) Run(t *testing.T, ctx context.Context) {
	r.daprd.WaitUntilRunning(t, ctx)

	t.Run("resiliency update via operator stream triggers SIGHUP reload", func(t *testing.T) {
		r.logOut.Reset()

		// Send a ResiliencyUpdate event on the operator stream.
		// The SIGHUP reconciler will receive this and send SIGHUP to daprd.
		close(r.resiliencySent)

		// After SIGHUP, daprd restarts.
		r.daprd.WaitUntilRunning(t, ctx)

		// Verify daprd restarted by checking for the startup log message
		// that appears after a SIGHUP restart.
		assert.EventuallyWithT(t, func(ct *assert.CollectT) {
			assert.True(ct, r.logOut.Contains("Resiliency"),
				"expected log to contain resiliency loading indication after SIGHUP")
		}, 15*time.Second, 10*time.Millisecond)
	})
}
