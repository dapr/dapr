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
	"github.com/stretchr/testify/require"

	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/log"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(nochange))
}

// nochange tests that when an update event is sent from the operator stream
// but the resource content has not actually changed, SIGHUP is NOT triggered.
type nochange struct {
	daprd    *daprd.Daprd
	operator *operator.Operator
	logOut   *log.Log

	configSendCh     chan []byte
	httpEndSendCh    chan []byte
	resiliencySendCh chan []byte
	configDone       atomic.Bool
	httpEndDone      atomic.Bool
	resiliencyDone   atomic.Bool
}

func (n *nochange) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	n.logOut = log.New()
	n.configSendCh = make(chan []byte, 1)
	n.httpEndSendCh = make(chan []byte, 1)
	n.resiliencySendCh = make(chan []byte, 1)

	snt := sentry.New(t)

	// Initial configuration with sampling rate 0
	initialConfigJSON, err := json.Marshal(map[string]any{
		"kind":       "Configuration",
		"apiVersion": "dapr.io/v1alpha1",
		"metadata":   map[string]any{"name": "hotreloading"},
		"spec": map[string]any{
			"features": []map[string]any{
				{"name": "HotReload", "enabled": true},
			},
			"tracing": map[string]any{
				"samplingRate": "0",
			},
		},
	})
	require.NoError(t, err)

	// Initial HTTP endpoint loaded at startup
	initialEndpointJSON, err := json.Marshal(map[string]any{
		"kind":       "HTTPEndpoint",
		"apiVersion": "dapr.io/v1alpha1",
		"metadata":   map[string]any{"name": "myendpoint", "namespace": "default"},
		"spec": map[string]any{
			"baseURL": "http://localhost:1234",
		},
	})
	require.NoError(t, err)

	// Initial resiliency loaded at startup
	initialResiliencyJSON, err := json.Marshal(map[string]any{
		"kind":       "Resiliency",
		"apiVersion": "dapr.io/v1alpha1",
		"metadata":   map[string]any{"name": "myresiliency", "namespace": "default"},
		"spec": map[string]any{
			"policies": map[string]any{
				"timeouts": map[string]any{
					"general": "5s",
				},
			},
		},
	})
	require.NoError(t, err)

	n.operator = operator.New(t,
		operator.WithSentry(snt),
		operator.WithGetConfigurationFn(func(_ context.Context, _ *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: initialConfigJSON,
			}, nil
		}),
		operator.WithListHTTPEndpointsFn(func(_ context.Context, _ *operatorv1.ListHTTPEndpointsRequest) (*operatorv1.ListHTTPEndpointsResponse, error) {
			return &operatorv1.ListHTTPEndpointsResponse{
				HttpEndpoints: [][]byte{initialEndpointJSON},
			}, nil
		}),
		operator.WithListResiliencyFn(func(_ context.Context, _ *operatorv1.ListResiliencyRequest) (*operatorv1.ListResiliencyResponse, error) {
			return &operatorv1.ListResiliencyResponse{
				Resiliencies: [][]byte{initialResiliencyJSON},
			}, nil
		}),
		operator.WithConfigurationUpdateFn(func(_ *operatorv1.ConfigurationUpdateRequest, srv operatorv1.Operator_ConfigurationUpdateServer) error {
			for {
				if n.configDone.Load() {
					<-srv.Context().Done()
					return errors.New("stream closed")
				}
				select {
				case <-srv.Context().Done():
					return nil
				case data := <-n.configSendCh:
					if err := srv.Send(&operatorv1.ConfigurationUpdateEvent{
						Configuration: data,
						Type:          operatorv1.ResourceEventType_UPDATED,
					}); err != nil {
						return err
					}
				}
			}
		}),
		operator.WithHTTPEndpointUpdateFn(func(_ *operatorv1.HTTPEndpointUpdateRequest, srv operatorv1.Operator_HTTPEndpointUpdateServer) error {
			for {
				if n.httpEndDone.Load() {
					<-srv.Context().Done()
					return errors.New("stream closed")
				}
				select {
				case <-srv.Context().Done():
					return nil
				case data := <-n.httpEndSendCh:
					if err := srv.Send(&operatorv1.HTTPEndpointUpdateEvent{
						HttpEndpoints: data,
						Type:          operatorv1.ResourceEventType_UPDATED,
					}); err != nil {
						return err
					}
				}
			}
		}),
		operator.WithResiliencyUpdateFn(func(_ *operatorv1.ResiliencyUpdateRequest, srv operatorv1.Operator_ResiliencyUpdateServer) error {
			for {
				if n.resiliencyDone.Load() {
					<-srv.Context().Done()
					return errors.New("stream closed")
				}
				select {
				case <-srv.Context().Done():
					return nil
				case data := <-n.resiliencySendCh:
					if err := srv.Send(&operatorv1.ResiliencyUpdateEvent{
						Resiliency: data,
						Type:       operatorv1.ResourceEventType_UPDATED,
					}); err != nil {
						return err
					}
				}
			}
		}),
	)

	n.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(snt.CABundle().X509.TrustAnchors)),
			exec.WithStdout(n.logOut),
			exec.WithStderr(n.logOut),
		),
		daprd.WithSentryAddress(snt.Address()),
		daprd.WithControlPlaneAddress(n.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
	)

	return []framework.Option{
		framework.WithProcesses(snt, n.operator, n.daprd),
	}
}

func (n *nochange) Run(t *testing.T, ctx context.Context) {
	n.daprd.WaitUntilRunning(t, ctx)

	t.Run("unchanged configuration does not trigger SIGHUP", func(t *testing.T) {
		n.logOut.Reset()

		// Send an update event with the SAME configuration content as loaded
		// at startup. The SIGHUP reconciler should detect it hasn't changed.
		unchangedConfig, err := json.Marshal(map[string]any{
			"kind":       "Configuration",
			"apiVersion": "dapr.io/v1alpha1",
			"metadata":   map[string]any{"name": "hotreloading"},
			"spec": map[string]any{
				"features": []map[string]any{
					{"name": "HotReload", "enabled": true},
				},
				"tracing": map[string]any{
					"samplingRate": "0",
				},
			},
		})
		require.NoError(t, err)
		n.configSendCh <- unchangedConfig
		n.configDone.Store(true)

		// Continuously verify no SIGHUP occurs over a bounded duration.
		assert.Never(t, func() bool {
			return n.logOut.Contains("Received signal 'hangup'")
		}, 2*time.Second, 100*time.Millisecond,
			"expected no SIGHUP when configuration has not changed")
	})

	t.Run("unchanged http endpoint does not trigger SIGHUP", func(t *testing.T) {
		n.logOut.Reset()

		// Send an update event with the SAME endpoint content as loaded at
		// startup.
		unchangedEndpoint, err := json.Marshal(map[string]any{
			"kind":       "HTTPEndpoint",
			"apiVersion": "dapr.io/v1alpha1",
			"metadata":   map[string]any{"name": "myendpoint", "namespace": "default"},
			"spec": map[string]any{
				"baseURL": "http://localhost:1234",
			},
		})
		require.NoError(t, err)
		n.httpEndSendCh <- unchangedEndpoint
		n.httpEndDone.Store(true)

		assert.Never(t, func() bool {
			return n.logOut.Contains("Received signal 'hangup'")
		}, 2*time.Second, 100*time.Millisecond,
			"expected no SIGHUP when http endpoint has not changed")
	})

	t.Run("unchanged resiliency does not trigger SIGHUP", func(t *testing.T) {
		n.logOut.Reset()

		// Send an update event with the SAME resiliency content as loaded at
		// startup.
		unchangedRes, err := json.Marshal(map[string]any{
			"kind":       "Resiliency",
			"apiVersion": "dapr.io/v1alpha1",
			"metadata":   map[string]any{"name": "myresiliency", "namespace": "default"},
			"spec": map[string]any{
				"policies": map[string]any{
					"timeouts": map[string]any{
						"general": "5s",
					},
				},
			},
		})
		require.NoError(t, err)
		n.resiliencySendCh <- unchangedRes
		n.resiliencyDone.Store(true)

		assert.Never(t, func() bool {
			return n.logOut.Contains("Received signal 'hangup'")
		}, 2*time.Second, 100*time.Millisecond,
			"expected no SIGHUP when resiliency has not changed")
	})
}
