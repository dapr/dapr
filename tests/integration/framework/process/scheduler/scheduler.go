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
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/modes"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/metrics"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/kit/ptr"
)

type Scheduler struct {
	exec       process.Interface
	ports      *ports.Ports
	httpClient *http.Client

	port        int
	healthzPort int
	metricsPort int

	namespace       string
	dataDir         string
	id              string
	initialCluster  string
	etcdClientPorts map[string]string
	sentry          *sentry.Sentry

	runOnce     sync.Once
	cleanupOnce sync.Once
}

func New(t *testing.T, fopts ...Option) *Scheduler {
	t.Helper()

	uid, err := uuid.NewUUID()
	require.NoError(t, err)

	uids := uid.String() + "-0"

	fp := ports.Reserve(t, 5)

	opts := options{
		logLevel:        "info",
		listenAddress:   "localhost",
		id:              uids,
		replicaCount:    1,
		port:            fp.Port(t),
		healthzPort:     fp.Port(t),
		metricsPort:     fp.Port(t),
		initialCluster:  uids + "=http://127.0.0.1:" + strconv.Itoa(fp.Port(t)),
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
		"--listen-address=" + opts.listenAddress,
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

	if opts.kubeconfig != nil {
		args = append(args, "--kubeconfig="+*opts.kubeconfig)
	}
	if opts.mode != nil {
		args = append(args, "--mode="+*opts.mode)
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
		httpClient:      client.HTTP(t),
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
	s.runOnce.Do(func() {
		s.ports.Free(t)
		s.exec.Run(t, ctx)
	})
}

func (s *Scheduler) Cleanup(t *testing.T) {
	s.cleanupOnce.Do(func() {
		s.exec.Cleanup(t)
	})
}

func (s *Scheduler) WaitUntilRunning(t *testing.T, ctx context.Context) {
	client := client.HTTP(t)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/healthz", s.healthzPort), nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		if !assert.NoError(c, err) {
			return
		}
		body, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.Equal(c, http.StatusOK, resp.StatusCode, string(body))
		assert.NoError(t, resp.Body.Close())
	}, time.Second*10, 10*time.Millisecond)
}

func (s *Scheduler) ID() string {
	return s.id
}

func (s *Scheduler) Port() int {
	return s.port
}

func (s *Scheduler) Address() string {
	return "127.0.0.1:" + strconv.Itoa(s.port)
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
	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, s.Address(),
		grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(math.MaxInt32), grpc.MaxCallRecvMsgSize(math.MaxInt32)),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(), grpc.WithReturnConnectionError(),
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

	id, err := spiffeid.FromSegments(sech.ControlPlaneTrustDomain(), "ns", s.namespace, "dapr-scheduler")
	require.NoError(t, err)

	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, s.Address(),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(math.MaxInt32), grpc.MaxCallSendMsgSize(math.MaxInt32)),
		sech.GRPCDialOptionMTLS(id),
		grpc.WithBlock(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	return schedulerv1pb.NewSchedulerClient(conn)
}

func (s *Scheduler) ipPort(port int) string {
	return "127.0.0.1:" + strconv.Itoa(port)
}

func (s *Scheduler) MetricsAddress() string {
	return s.ipPort(s.MetricsPort())
}

// Metrics returns a subset of metrics scraped from the metrics endpoint
func (s *Scheduler) Metrics(t *testing.T, ctx context.Context) *metrics.Metrics {
	t.Helper()

	return metrics.New(t, ctx, fmt.Sprintf("http://%s/metrics", s.MetricsAddress()))
}

func (s *Scheduler) ETCDClient(t *testing.T) *clientv3.Client {
	t.Helper()

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{"127.0.0.1:" + s.EtcdClientPort()},
		DialTimeout: 40 * time.Second,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, client.Close())
	})

	return client
}

func (s *Scheduler) EtcdJobs(t *testing.T, ctx context.Context) []*mvccpb.KeyValue {
	t.Helper()
	resp, err := s.ETCDClient(t).KV.Get(ctx, "dapr/jobs", clientv3.WithPrefix())
	require.NoError(t, err)
	return resp.Kvs
}

func (s *Scheduler) WatchJobs(t *testing.T, ctx context.Context, initial *schedulerv1pb.WatchJobsRequestInitial, respStatus *atomic.Value) <-chan string {
	t.Helper()

	watchErr := make(chan error)
	t.Cleanup(func() {
		select {
		case err := <-watchErr:
			require.NoError(t, err)
		case <-time.After(time.Second * 5):
			assert.Fail(t, "failed to close watcher")
		}
	})

	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)

	watch, err := s.Client(t, ctx).WatchJobs(ctx)
	require.NoError(t, err)

	require.NoError(t, watch.Send(&schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{Initial: initial},
	}))

	ch := make(chan string)

	go func() {
		defer func() {
			watchErr <- watch.CloseSend()
		}()
		for {
			resp, err := watch.Recv()
			if cerr := status.Code(err); cerr == codes.Canceled || cerr == codes.DeadlineExceeded {
				return
			}
			if !assert.NoError(t, err) {
				return
			}
			err = watch.Send(&schedulerv1pb.WatchJobsRequest{
				WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
					Result: &schedulerv1pb.WatchJobsRequestResult{
						Id:     resp.GetId(),
						Status: respStatus.Load().(schedulerv1pb.WatchJobsRequestResultStatus),
					},
				},
			})
			if !errors.Is(err, io.EOF) {
				assert.NoError(t, err)
			}
			select {
			case ch <- resp.GetName():
			case <-ctx.Done():
			}
		}
	}()

	return ch
}

func (s *Scheduler) WatchJobsSuccess(t *testing.T, ctx context.Context, initial *schedulerv1pb.WatchJobsRequestInitial) <-chan string {
	t.Helper()

	var status atomic.Value
	status.Store(schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS)
	return s.WatchJobs(t, ctx, initial, &status)
}

func (s *Scheduler) WatchJobsFailed(t *testing.T, ctx context.Context, initial *schedulerv1pb.WatchJobsRequestInitial) <-chan string {
	t.Helper()

	var status atomic.Value
	status.Store(schedulerv1pb.WatchJobsRequestResultStatus_FAILED)
	return s.WatchJobs(t, ctx, initial, &status)
}

func (s *Scheduler) JobNowJob(name, namespace, appID string) *schedulerv1pb.ScheduleJobRequest {
	return &schedulerv1pb.ScheduleJobRequest{
		Name: name,
		Job:  &schedulerv1pb.Job{DueTime: ptr.Of(time.Now().Format(time.RFC3339))},
		Metadata: &schedulerv1pb.JobMetadata{
			Namespace: namespace, AppId: appID,
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: new(schedulerv1pb.JobTargetMetadata_Job),
			},
		},
	}
}

func (s *Scheduler) JobNowActor(name, namespace, appID, actorType, actorID string) *schedulerv1pb.ScheduleJobRequest {
	return &schedulerv1pb.ScheduleJobRequest{
		Name: name,
		Job:  &schedulerv1pb.Job{DueTime: ptr.Of(time.Now().Format(time.RFC3339))},
		Metadata: &schedulerv1pb.JobMetadata{
			Namespace: namespace, AppId: appID,
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{
						Type: actorType, Id: actorID,
					},
				},
			},
		},
	}
}

func (s *Scheduler) ListJobJobs(t *testing.T, ctx context.Context, namespace, appID string) *schedulerv1pb.ListJobsResponse {
	t.Helper()
	resp, err := s.Client(t, ctx).ListJobs(ctx, &schedulerv1pb.ListJobsRequest{
		Metadata: &schedulerv1pb.JobMetadata{
			Namespace: namespace, AppId: appID,
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{
					Job: new(schedulerv1pb.TargetJob),
				},
			},
		},
	})
	require.NoError(t, err)
	return resp
}

func (s *Scheduler) ListJobActors(t *testing.T, ctx context.Context, namespace, appID, actorType, actorID string) *schedulerv1pb.ListJobsResponse {
	t.Helper()
	resp, err := s.Client(t, ctx).ListJobs(ctx, &schedulerv1pb.ListJobsRequest{
		Metadata: &schedulerv1pb.JobMetadata{
			Namespace: namespace, AppId: appID,
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{
						Type: actorType, Id: actorID,
					},
				},
			},
		},
	})
	require.NoError(t, err)
	return resp
}

func (s *Scheduler) ListAllKeys(t *testing.T, ctx context.Context, prefix string) []string {
	t.Helper()

	resp, err := client.Etcd(t, clientv3.Config{
		Endpoints:   []string{"127.0.0.1:" + s.EtcdClientPort()},
		DialTimeout: 40 * time.Second,
	}).ListAllKeys(ctx, prefix)
	require.NoError(t, err)

	return resp
}
