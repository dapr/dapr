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

package operator

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/modes"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/util"
)

type Operator struct {
	exec     process.Interface
	freeport *util.FreePort

	port        int
	metricsPort int
	healthzPort int
	namespace   string
}

func New(t *testing.T, fopts ...Option) *Operator {
	t.Helper()

	fp := util.ReservePorts(t, 3)
	opts := options{
		logLevel:              "info",
		disableLeaderElection: true,
		port:                  fp.Port(t, 0),
		metricsPort:           fp.Port(t, 1),
		healthzPort:           fp.Port(t, 2),
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	require.NotNil(t, opts.trustAnchorsFile, "trustAnchorsFile is required")
	require.NotNil(t, opts.kubeconfigPath, "kubeconfigPath is required")
	require.NotNil(t, opts.namespace, "namespace is required")

	args := []string{
		"-log-level=" + opts.logLevel,
		"-port=" + strconv.Itoa(opts.port),
		"-healthz-port=" + strconv.Itoa(opts.healthzPort),
		"-metrics-port=" + strconv.Itoa(opts.metricsPort),
		"-trust-anchors-file=" + *opts.trustAnchorsFile,
		"-disable-leader-election=" + strconv.FormatBool(opts.disableLeaderElection),
		"-kubeconfig=" + *opts.kubeconfigPath,
	}

	if opts.configPath != nil {
		args = append(args, "-config-path="+*opts.configPath)
	}

	return &Operator{
		exec: exec.New(t,
			binary.EnvValue("operator"), args,
			append(
				opts.execOpts,
				exec.WithEnvVars(t,
					"KUBERNETES_SERVICE_HOST", "anything",
					"NAMESPACE", *opts.namespace,
				),
			)...,
		),
		freeport:    fp,
		port:        opts.port,
		metricsPort: opts.metricsPort,
		healthzPort: opts.healthzPort,
		namespace:   *opts.namespace,
	}
}

func (o *Operator) Run(t *testing.T, ctx context.Context) {
	o.freeport.Free(t)
	o.exec.Run(t, ctx)
}

func (o *Operator) Cleanup(t *testing.T) {
	o.exec.Cleanup(t)
}

func (o *Operator) WaitUntilRunning(t *testing.T, ctx context.Context) {
	client := util.HTTPClient(t)
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
	}, time.Second*5, 100*time.Millisecond)
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

func (o *Operator) Dial(t *testing.T, ctx context.Context, ns string, sentry *sentry.Sentry) operatorv1pb.OperatorClient {
	sec, err := security.New(ctx, security.Options{
		SentryAddress:           "localhost:" + strconv.Itoa(sentry.Port()),
		ControlPlaneTrustDomain: "integration.test.dapr.io",
		ControlPlaneNamespace:   ns,
		TrustAnchorsFile:        sentry.TrustAnchorsFile(t),
		AppID:                   "myapp",
		Mode:                    modes.StandaloneMode,
		MTLSEnabled:             true,
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

	conn, err := grpc.DialContext(ctx, "localhost:"+strconv.Itoa(o.Port()), sech.GRPCDialOptionMTLS(id))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	return operatorv1pb.NewOperatorClient(conn)
}
