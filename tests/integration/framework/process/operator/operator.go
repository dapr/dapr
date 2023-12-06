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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/util"
)

type Operator struct {
	exec     process.Interface
	freeport *util.FreePort

	port        int
	metricsPort int
	healthzPort int
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
	require.NotNil(t, opts.trustAnchorsFile, "trustAnchorsFile is required")
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
				exec.WithEnvVars("KUBERNETES_SERVICE_HOST", "anything"),
				exec.WithEnvVars("NAMESPACE", *opts.namespace),
			)...,
		),
		freeport:    fp,
		port:        opts.port,
		metricsPort: opts.metricsPort,
		healthzPort: opts.healthzPort,
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
