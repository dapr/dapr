/*
Copyright 2023 The Dapr Authors
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

package helm

import (
	"context"
	"fmt"
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/modes"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/kit/ptr"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/require"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// Helm test helm chart template
type Helm struct {
	exec process.Interface
}

func New(t *testing.T, fopts ...Option) *Helm {
	t.Helper()

	var opts options

	for _, fopt := range fopts {
		fopt(&opts)
	}

	args := opts.getHelmArgs()

	return &Helm{
		exec: exec.New(t,
			"helm template", args,
			append(
				opts.execOpts,
			)...,
		),
	}
}

func (o *Helm) Run(t *testing.T, ctx context.Context) {
	o.exec.Run(t, ctx)
}

func (o *Operator) Cleanup(t *testing.T) {
	o.exec.Cleanup(t)
}

func (o *Operator) WaitUntilRunning(t *testing.T, ctx context.Context) {
	client := client.HTTP(t)
	assert.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/healthz", o.healthzPort), nil)
		if err != nil {
			return false
		}
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return http.StatusOK == resp.StatusCode
	}, time.Second*10, 10*time.Millisecond)
}

func (o *Operator) Port() int {
	return o.port
}

func (o *Operator) Address() string {
	return "localhost:" + strconv.Itoa(o.port)
}

func (o *Operator) MetricsPort() int {
	return o.metricsPort
}

func (o *Operator) HealthzPort() int {
	return o.healthzPort
}

func (o *Operator) Dial(t *testing.T, ctx context.Context, sentry *sentry.Sentry, appID string) operatorv1pb.OperatorClient {
	sec, err := security.New(ctx, security.Options{
		SentryAddress:           "localhost:" + strconv.Itoa(sentry.Port()),
		ControlPlaneTrustDomain: "integration.test.dapr.io",
		ControlPlaneNamespace:   o.namespace,
		TrustAnchorsFile:        ptr.Of(sentry.TrustAnchorsFile(t)),
		AppID:                   appID,
		Mode:                    modes.StandaloneMode,
		MTLSEnabled:             true,
		Healthz:                 healthz.New(),
	})
	require.NoError(t, err)

	errCh := make(chan error)
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		errCh <- sec.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-errCh)
	})

	sech, err := sec.Handler(ctx)
	require.NoError(t, err)

	id, err := spiffeid.FromSegments(sech.ControlPlaneTrustDomain(), "ns", o.namespace, "dapr-operator")
	require.NoError(t, err)
	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, "localhost:"+strconv.Itoa(o.Port()), sech.GRPCDialOptionMTLS(id))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	return operatorv1pb.NewOperatorClient(conn)
}
