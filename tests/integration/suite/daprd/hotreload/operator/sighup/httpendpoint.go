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
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(httpendpoint))
}

// httpendpoint tests that an HTTPEndpointUpdate event from the operator
// triggers a SIGHUP restart of daprd. After restart, the new HTTP endpoint
// appears in metadata.
type httpendpoint struct {
	daprd    *daprd.Daprd
	operator *operator.Operator

	endpointSent chan struct{}
	eventSent    atomic.Bool
	hasEndpoint  atomic.Bool
}

func (h *httpendpoint) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	h.endpointSent = make(chan struct{})

	endpointJSON, err := json.Marshal(map[string]any{
		"kind":       "HTTPEndpoint",
		"apiVersion": "dapr.io/v1alpha1",
		"metadata":   map[string]any{"name": "myendpoint"},
		"spec": map[string]any{
			"baseUrl": "http://localhost:1234",
		},
	})
	require.NoError(t, err)

	snt := sentry.New(t)

	h.operator = operator.New(t,
		operator.WithSentry(snt),
		operator.WithGetConfigurationFn(func(_ context.Context, _ *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: []byte(
					`{"kind":"Configuration","apiVersion":"dapr.io/v1alpha1","metadata":{"name":"hotreloading"},"spec":{"features":[{"name":"HotReload","enabled":true}]}}`,
				),
			}, nil
		}),
		operator.WithListHTTPEndpointsFn(func(_ context.Context, _ *operatorv1.ListHTTPEndpointsRequest) (*operatorv1.ListHTTPEndpointsResponse, error) {
			if h.hasEndpoint.Load() {
				return &operatorv1.ListHTTPEndpointsResponse{
					HttpEndpoints: [][]byte{endpointJSON},
				}, nil
			}
			return new(operatorv1.ListHTTPEndpointsResponse), nil
		}),
		operator.WithHTTPEndpointUpdateFn(func(_ *operatorv1.HTTPEndpointUpdateRequest, srv operatorv1.Operator_HTTPEndpointUpdateServer) error {
			// Only send the event on the first stream connection.
			// After SIGHUP, daprd reconnects and we must not re-send
			// the event or it will trigger another SIGHUP loop.
			if !h.eventSent.Load() {
				select {
				case <-srv.Context().Done():
					return nil
				case <-h.endpointSent:
				}
				h.eventSent.Store(true)
				if err := srv.Send(&operatorv1.HTTPEndpointUpdateEvent{
					HttpEndpoints: endpointJSON,
					Type:          operatorv1.ResourceEventType_CREATED,
				}); err != nil {
					return err
				}
			}
			<-srv.Context().Done()
			return errors.New("stream closed")
		}),
	)

	h.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(snt.CABundle().X509.TrustAnchors)),
		),
		daprd.WithSentryAddress(snt.Address()),
		daprd.WithControlPlaneAddress(h.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
	)

	return []framework.Option{
		framework.WithProcesses(snt, h.operator, h.daprd),
	}
}

func (h *httpendpoint) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)

	t.Run("no HTTP endpoints initially", func(t *testing.T) {
		assert.Empty(t, h.daprd.GetMetaHTTPEndpoints(t, ctx))
	})

	t.Run("HTTP endpoint update via operator stream triggers SIGHUP reload", func(t *testing.T) {
		// Update the list response so that after SIGHUP restart, daprd
		// will get the endpoint when it calls ListHTTPEndpoints.
		h.hasEndpoint.Store(true)

		// Send an HTTPEndpointUpdate event on the operator stream.
		// The SIGHUP reconciler receives this and sends SIGHUP to daprd.
		close(h.endpointSent)

		// After SIGHUP, daprd restarts and loads the endpoint.
		h.daprd.WaitUntilRunning(t, ctx)

		require.EventuallyWithT(t, func(ct *assert.CollectT) {
			endpoints := h.daprd.GetMetaHTTPEndpoints(ct, ctx)
			assert.Len(ct, endpoints, 1)
		}, 15*time.Second, 100*time.Millisecond)

		assert.ElementsMatch(t, []*rtv1.MetadataHTTPEndpoint{
			{Name: "myendpoint"},
		}, h.daprd.GetMetaHTTPEndpoints(t, ctx))
	})
}
