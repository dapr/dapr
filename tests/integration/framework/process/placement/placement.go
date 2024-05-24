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

package placement

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/util"
)

type Placement struct {
	exec     process.Interface
	freeport *util.FreePort
	running  atomic.Bool

	id                  string
	port                int
	healthzPort         int
	metricsPort         int
	initialCluster      string
	initialClusterPorts []int
}

func New(t *testing.T, fopts ...Option) *Placement {
	t.Helper()

	uid, err := uuid.NewUUID()
	require.NoError(t, err)

	fp := util.ReservePorts(t, 4)
	opts := options{
		id:                  uid.String(),
		logLevel:            "info",
		port:                fp.Port(t, 0),
		healthzPort:         fp.Port(t, 1),
		metricsPort:         fp.Port(t, 2),
		initialCluster:      uid.String() + "=127.0.0.1:" + strconv.Itoa(fp.Port(t, 3)),
		initialClusterPorts: []int{fp.Port(t, 3)},
		maxAPILevel:         -1,
		minAPILevel:         0,
		metadataEnabled:     false,
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	args := []string{
		"--log-level=" + opts.logLevel,
		"--id=" + opts.id,
		"--port=" + strconv.Itoa(opts.port),
		"--healthz-port=" + strconv.Itoa(opts.healthzPort),
		"--metrics-port=" + strconv.Itoa(opts.metricsPort),
		"--initial-cluster=" + opts.initialCluster,
		"--tls-enabled=" + strconv.FormatBool(opts.tlsEnabled),
		"--max-api-level=" + strconv.Itoa(opts.maxAPILevel),
		"--min-api-level=" + strconv.Itoa(opts.minAPILevel),
		"--metadata-enabled=" + strconv.FormatBool(opts.metadataEnabled),
	}
	if opts.sentryAddress != nil {
		args = append(args, "--sentry-address="+*opts.sentryAddress)
	}
	if opts.trustAnchorsFile != nil {
		args = append(args, "--trust-anchors-file="+*opts.trustAnchorsFile)
	}

	return &Placement{
		exec:                exec.New(t, binary.EnvValue("placement"), args, opts.execOpts...),
		freeport:            fp,
		id:                  opts.id,
		port:                opts.port,
		healthzPort:         opts.healthzPort,
		metricsPort:         opts.metricsPort,
		initialCluster:      opts.initialCluster,
		initialClusterPorts: opts.initialClusterPorts,
	}
}

func (p *Placement) Run(t *testing.T, ctx context.Context) {
	if !p.running.CompareAndSwap(false, true) {
		t.Fatal("Process is already running")
	}

	p.freeport.Free(t)
	p.exec.Run(t, ctx)
}

func (p *Placement) Cleanup(t *testing.T) {
	if !p.running.CompareAndSwap(true, false) {
		return
	}

	p.exec.Cleanup(t)
}

func (p *Placement) WaitUntilRunning(t *testing.T, ctx context.Context) {
	client := util.HTTPClient(t)
	assert.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/healthz", p.healthzPort), nil)
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

func (p *Placement) ID() string {
	return p.id
}

func (p *Placement) Port() int {
	return p.port
}

func (p *Placement) Address() string {
	return "127.0.0.1:" + strconv.Itoa(p.port)
}

func (p *Placement) HealthzPort() int {
	return p.healthzPort
}

func (p *Placement) MetricsPort() int {
	return p.metricsPort
}

func (p *Placement) InitialCluster() string {
	return p.initialCluster
}

func (p *Placement) InitialClusterPorts() []int {
	return p.initialClusterPorts
}

func (p *Placement) CurrentActorsAPILevel() int {
	return 20 // Defined in pkg/actors/internal/api_level.go
}
