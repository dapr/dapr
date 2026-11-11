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
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(httpendpointlisterror))
}

type httpendpointlisterror struct {
	daprd  *daprd.Daprd
	logOut *log.Log
}

func (h *httpendpointlisterror) Setup(t *testing.T) []framework.Option {
	h.logOut = log.New()

	loaded, err := json.Marshal(map[string]any{
		"kind":       "HTTPEndpoint",
		"apiVersion": "dapr.io/v1alpha1",
		"metadata":   map[string]any{"name": "myendpoint", "namespace": "default"},
		"spec":       map[string]any{"baseUrl": "http://localhost:1234"},
	})
	require.NoError(t, err)

	var lists atomic.Int32
	snt := sentry.New(t)
	op := operator.New(t,
		operator.WithSentry(snt),
		operator.WithListHTTPEndpointsFn(func(context.Context, *operatorv1.ListHTTPEndpointsRequest) (*operatorv1.ListHTTPEndpointsResponse, error) {
			if lists.Add(1) == 1 {
				return &operatorv1.ListHTTPEndpointsResponse{HttpEndpoints: [][]byte{loaded}}, nil
			}
			return nil, errors.New("secret dapr-tls-certificates not found")
		}),
	)

	h.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(snt.CABundle().X509.TrustAnchors)),
			exec.WithStdout(h.logOut),
			exec.WithStderr(h.logOut),
		),
		daprd.WithSentryAddress(snt.Address()),
		daprd.WithControlPlaneAddress(op.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
	)

	return []framework.Option{
		framework.WithProcesses(snt, op, h.daprd),
	}
}

func (h *httpendpointlisterror) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, h.logOut.Contains("Starting to watch HTTPEndpoint updates for SIGHUP reload"))
	}, time.Second*10, time.Millisecond*10)
}
