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
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/dapr/pkg/modes"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/kit/ptr"
)

type Scheduler struct {
	exec    process.Interface
	ports   *ports.Ports
	running atomic.Bool

	port        int
	healthzPort int
	metricsPort int

	namespace       string
	dataDir         string
	id              string
	initialCluster  string
	etcdClientPorts map[string]string
	sentry          *sentry.Sentry
}

func New(t *testing.T, fopts ...Option) *Scheduler {
	t.Helper()

	uid, err := uuid.NewUUID()
	require.NoError(t, err)

	uids := uid.String() + "-0"

	fp := ports.Reserve(t, 5)
	port1 := fp.Port(t)

	opts := options{
		logLevel:        "info",
		id:              uids,
		replicaCount:    1,
		port:            fp.Port(t),
		healthzPort:     fp.Port(t),
		metricsPort:     fp.Port(t),
		initialCluster:  uids + "=http://localhost:" + strconv.Itoa(port1),
		etcdClientPorts: []string{uids + "=" + strconv.Itoa(fp.Port(t))},
		namespace:       "default",
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	var dataDir string
	if opts.dataDir != nil {
		dataDir = *opts.dataDir
	} else {
		dataDir = t.TempDir()
		require.NoError(t, os.Chmod(dataDir, 0o700))
	}

	args := []string{
		"--log-level=" + opts.logLevel,
		"--id=" + opts.id,
		"--replica-count=" + strconv.FormatUint(uint64(opts.replicaCount), 10),
		"--port=" + strconv.Itoa(opts.port),
		"--healthz-port=" + strconv.Itoa(opts.healthzPort),
		"--metrics-port=" + strconv.Itoa(opts.metricsPort),
		"--initial-cluster=" + opts.initialCluster,
		"--etcd-data-dir=" + dataDir,
		"--etcd-client-ports=" + strings.Join(opts.etcdClientPorts, ","),
	}

	if opts.listenAddress != nil {
		args = append(args, "--listen-address="+*opts.listenAddress)
	}
	if opts.sentry != nil {
		taFile := filepath.Join(t.TempDir(), "ca.pem")
		require.NoError(t, os.WriteFile(taFile, opts.sentry.CABundle().TrustAnchors, 0o600))
		args = append(args,
			"--tls-enabled=true",
			"--sentry-address="+opts.sentry.Address(),
			"--trust-anchors-file="+taFile,
			"--trust-domain="+opts.sentry.TrustDomain(t),
		)
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
		exec: exec.New(t, binary.EnvValue("scheduler"), args,
			append(opts.execOpts, exec.WithEnvVars(t,
				"NAMESPACE", opts.namespace,
			))...,
		),
		ports:           fp,
		id:              opts.id,
		port:            opts.port,
		healthzPort:     opts.healthzPort,
		metricsPort:     opts.metricsPort,
		initialCluster:  opts.initialCluster,
		etcdClientPorts: clientPorts,
		dataDir:         dataDir,
		sentry:          opts.sentry,
		namespace:       opts.namespace,
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

func (s *Scheduler) DataDir() string {
	return s.dataDir
}

func (s *Scheduler) Client(t *testing.T, ctx context.Context) schedulerv1pb.SchedulerClient {
	conn, err := grpc.DialContext(ctx, s.Address(), grpc.WithBlock(), grpc.WithReturnConnectionError(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	return schedulerv1pb.NewSchedulerClient(conn)
}

func (s *Scheduler) ClientMTLS(t *testing.T, ctx context.Context, appID string) schedulerv1pb.SchedulerClient {
	t.Helper()

	require.NotNil(t, s.sentry)

	sec, err := security.New(ctx, security.Options{
		SentryAddress:           "localhost:" + strconv.Itoa(s.sentry.Port()),
		ControlPlaneTrustDomain: s.sentry.TrustDomain(t),
		ControlPlaneNamespace:   s.sentry.Namespace(),
		TrustAnchorsFile:        ptr.Of(s.sentry.TrustAnchorsFile(t)),
		AppID:                   appID,
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

	id, err := spiffeid.FromSegments(sech.ControlPlaneTrustDomain(), "ns", s.namespace, "dapr-scheduler")
	require.NoError(t, err)

	conn, err := grpc.DialContext(ctx, s.Address(), sech.GRPCDialOptionMTLS(id), grpc.WithBlock())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	return schedulerv1pb.NewSchedulerClient(conn)
}
