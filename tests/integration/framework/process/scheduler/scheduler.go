/*
Copyright 2024 The Dapr Authors
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

package scheduler

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/util"
)

type Scheduler struct {
	exec    process.Interface
	ports   *ports.Ports
	running atomic.Bool

	port        int
	healthzPort int
	metricsPort int

	dataDir             string
	id                  string
	initialCluster      string
	initialClusterPorts []int
	etcdClientPorts     map[string]string
}

func New(t *testing.T, fopts ...Option) *Scheduler {
	t.Helper()

	uid, err := uuid.NewUUID()
	require.NoError(t, err)

	uids := uid.String() + "-0"

	fp := ports.Reserve(t, 5)
	port1 := fp.Port(t)

	opts := options{
		logLevel:            "info",
		id:                  uids,
		replicaCount:        1,
		port:                fp.Port(t),
		healthzPort:         fp.Port(t),
		metricsPort:         fp.Port(t),
		initialCluster:      uids + "=http://localhost:" + strconv.Itoa(port1),
		initialClusterPorts: []int{port1},
		etcdClientPorts:     []string{uids + "=" + strconv.Itoa(fp.Port(t))},
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	tmpDir := t.TempDir()

	require.NoError(t, os.Chmod(tmpDir, 0o700))

	args := []string{
		"--log-level=" + opts.logLevel,
		"--id=" + opts.id,
		"--replica-count=" + strconv.FormatUint(uint64(opts.replicaCount), 10),
		"--port=" + strconv.Itoa(opts.port),
		"--healthz-port=" + strconv.Itoa(opts.healthzPort),
		"--metrics-port=" + strconv.Itoa(opts.metricsPort),
		"--initial-cluster=" + opts.initialCluster,
		"--tls-enabled=" + strconv.FormatBool(opts.tlsEnabled),
		"--etcd-data-dir=" + tmpDir,
		"--etcd-client-ports=" + strings.Join(opts.etcdClientPorts, ","),
	}

	if opts.listenAddress != nil {
		args = append(args, "--listen-address="+*opts.listenAddress)
	}
	if opts.sentryAddress != nil {
		args = append(args, "--sentry-address="+*opts.sentryAddress)
	}
	if opts.trustAnchorsFile != nil {
		args = append(args, "--trust-anchors-file="+*opts.trustAnchorsFile)
	}

	clientPorts := make(map[string]string)
	for _, input := range opts.etcdClientPorts {
		idAndPort := strings.Split(input, "=")
		require.Len(t, idAndPort, 2)

		schedulerID := strings.TrimSpace(idAndPort[0])
		port := strings.TrimSpace(idAndPort[1])
		clientPorts[schedulerID] = port
	}

	return &Scheduler{
		exec:                exec.New(t, binary.EnvValue("scheduler"), args, opts.execOpts...),
		ports:               fp,
		id:                  opts.id,
		port:                opts.port,
		healthzPort:         opts.healthzPort,
		metricsPort:         opts.metricsPort,
		initialCluster:      opts.initialCluster,
		initialClusterPorts: opts.initialClusterPorts,
		etcdClientPorts:     clientPorts,
		dataDir:             tmpDir,
	}
}

func (s *Scheduler) Run(t *testing.T, ctx context.Context) {
	if !s.running.CompareAndSwap(false, true) {
		t.Fatal("Process is already running")
	}

	s.ports.Free(t)
	s.exec.Run(t, ctx)
}

func (s *Scheduler) Cleanup(t *testing.T) {
	if !s.running.CompareAndSwap(true, false) {
		return
	}

	s.exec.Cleanup(t)
}

func (s *Scheduler) WaitUntilRunning(t *testing.T, ctx context.Context) {
	client := util.HTTPClient(t)

	assert.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/healthz", s.healthzPort), nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return http.StatusOK == resp.StatusCode
	}, time.Second*5, 10*time.Millisecond)
}

func (s *Scheduler) ID() string {
	return s.id
}

func (s *Scheduler) Port() int {
	return s.port
}

func (s *Scheduler) Address() string {
	return "localhost:" + strconv.Itoa(s.port)
}

func (s *Scheduler) HealthzPort() int {
	return s.healthzPort
}

func (s *Scheduler) MetricsPort() int {
	return s.metricsPort
}

func (s *Scheduler) InitialCluster() string {
	return s.initialCluster
}

func (s *Scheduler) EtcdClientPort() string {
	return s.etcdClientPorts[s.id]
}

func (s *Scheduler) InitialClusterPorts() []int {
	return s.initialClusterPorts
}

func (s *Scheduler) DataDir() string {
	return s.dataDir
}
