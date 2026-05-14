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

package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
)

// Method names used with ArmFailures. Using typed constants keeps tests
// from drifting if a method is renamed.
const (
	MethodScheduleJob        = "ScheduleJob"
	MethodDeleteJob          = "DeleteJob"
	MethodGetJob             = "GetJob"
	MethodListJobs           = "ListJobs"
	MethodDeleteByMetadata   = "DeleteByMetadata"
	MethodDeleteByNamePrefix = "DeleteByNamePrefix"
)

type Proxy struct {
	schedulerv1pb.UnimplementedSchedulerServer

	sched *scheduler.Scheduler

	ports    *ports.Ports
	port     int
	listener net.Listener
	grpcSrv  *grpc.Server
	upstream *grpc.ClientConn
	client   schedulerv1pb.SchedulerClient

	runOnce  sync.Once
	done     chan struct{}
	serveErr chan error

	mu       sync.Mutex
	armed    map[string]armConfig
	failures atomic.Int32
}

// armConfig captures the per-method failure injection state.
type armConfig struct {
	remaining int
	code      codes.Code
	notify    chan struct{}
}

// New returns a proxy that wraps the given scheduler. The proxy waits for
// the upstream to be reachable on Run, so framework process ordering
// relative to the scheduler is not required. daprd should be configured
// with daprd.WithSchedulerAddresses(proxy.Address()) instead of pointing at
// the scheduler directly.
func New(t *testing.T, sched *scheduler.Scheduler) *Proxy {
	t.Helper()
	fp := ports.Reserve(t, 1)
	return &Proxy{
		sched:    sched,
		ports:    fp,
		port:     fp.Port(t),
		armed:    make(map[string]armConfig),
		done:     make(chan struct{}),
		serveErr: make(chan error, 1),
	}
}

func (p *Proxy) Run(t *testing.T, ctx context.Context) {
	p.runOnce.Do(func() {
		p.sched.WaitUntilRunning(t, ctx)

		//nolint:staticcheck
		conn, err := grpc.DialContext(ctx, p.sched.Address(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
			grpc.WithReturnConnectionError(),
		)
		require.NoError(t, err)
		p.upstream = conn
		p.client = schedulerv1pb.NewSchedulerClient(conn)

		p.ports.Free(t)
		lis, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p.port))
		require.NoError(t, err)
		p.listener = lis

		p.grpcSrv = grpc.NewServer()
		schedulerv1pb.RegisterSchedulerServer(p.grpcSrv, p)

		go func() {
			p.serveErr <- p.grpcSrv.Serve(lis)
			close(p.done)
		}()
	})
}

func (p *Proxy) Cleanup(t *testing.T) {
	if p.grpcSrv != nil {
		p.grpcSrv.GracefulStop()
		<-p.done
		if err := <-p.serveErr; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			require.NoError(t, err)
		}
	}
	if p.upstream != nil {
		require.NoError(t, p.upstream.Close())
	}
}

func (p *Proxy) Port() int        { return p.port }
func (p *Proxy) Address() string  { return "127.0.0.1:" + strconv.Itoa(p.port) }
func (p *Proxy) FailedCount() int { return int(p.failures.Load()) }

// ArmFailures arms the proxy to fail the next n requests to the given method
// with the supplied gRPC status code. n=0 disarms. If notify is non-nil it is
// closed the first time a matching call is failed.
//
// Choose the code with care: the daprd-side CreateReminderWithRetry
// transparently retries codes.Unavailable / codes.DeadlineExceeded with
// exponential backoff (up to a minute), so injecting those codes only stalls
// the call instead of surfacing the failure. Use codes.Internal / codes.Aborted
// or another non-transient code when the test wants the failure to propagate
// to the orchestrator's error path.
func (p *Proxy) ArmFailures(method string, n int, code codes.Code, notify chan struct{}) {
	p.mu.Lock()
	if n <= 0 {
		delete(p.armed, method)
	} else {
		p.armed[method] = armConfig{remaining: n, code: code, notify: notify}
	}
	p.mu.Unlock()
}

func (p *Proxy) takeFailure(method string) (codes.Code, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cfg, ok := p.armed[method]
	if !ok || cfg.remaining <= 0 {
		return 0, false
	}
	cfg.remaining--
	notify := cfg.notify
	cfg.notify = nil
	if cfg.remaining == 0 {
		delete(p.armed, method)
	} else {
		p.armed[method] = cfg
	}
	p.failures.Add(1)
	if notify != nil {
		close(notify)
	}
	return cfg.code, true
}

func injected(method string, code codes.Code) error {
	return status.Errorf(code, "proxy.Scheduler: injected failure for %s", method)
}

func (p *Proxy) ScheduleJob(ctx context.Context, req *schedulerv1pb.ScheduleJobRequest) (*schedulerv1pb.ScheduleJobResponse, error) {
	if code, ok := p.takeFailure(MethodScheduleJob); ok {
		return nil, injected(MethodScheduleJob, code)
	}
	return p.client.ScheduleJob(ctx, req)
}

func (p *Proxy) DeleteJob(ctx context.Context, req *schedulerv1pb.DeleteJobRequest) (*schedulerv1pb.DeleteJobResponse, error) {
	if code, ok := p.takeFailure(MethodDeleteJob); ok {
		return nil, injected(MethodDeleteJob, code)
	}
	return p.client.DeleteJob(ctx, req)
}

func (p *Proxy) GetJob(ctx context.Context, req *schedulerv1pb.GetJobRequest) (*schedulerv1pb.GetJobResponse, error) {
	if code, ok := p.takeFailure(MethodGetJob); ok {
		return nil, injected(MethodGetJob, code)
	}
	return p.client.GetJob(ctx, req)
}

func (p *Proxy) ListJobs(ctx context.Context, req *schedulerv1pb.ListJobsRequest) (*schedulerv1pb.ListJobsResponse, error) {
	if code, ok := p.takeFailure(MethodListJobs); ok {
		return nil, injected(MethodListJobs, code)
	}
	return p.client.ListJobs(ctx, req)
}

func (p *Proxy) DeleteByMetadata(ctx context.Context, req *schedulerv1pb.DeleteByMetadataRequest) (*schedulerv1pb.DeleteByMetadataResponse, error) {
	if code, ok := p.takeFailure(MethodDeleteByMetadata); ok {
		return nil, injected(MethodDeleteByMetadata, code)
	}
	return p.client.DeleteByMetadata(ctx, req)
}

func (p *Proxy) DeleteByNamePrefix(ctx context.Context, req *schedulerv1pb.DeleteByNamePrefixRequest) (*schedulerv1pb.DeleteByNamePrefixResponse, error) {
	if code, ok := p.takeFailure(MethodDeleteByNamePrefix); ok {
		return nil, injected(MethodDeleteByNamePrefix, code)
	}
	return p.client.DeleteByNamePrefix(ctx, req)
}

// WatchJobs bidirectionally forwards messages between the daprd-side stream
// and the upstream scheduler stream. When either direction errors we cancel
// the shared context to unblock the other goroutine and drain its error so
// no goroutine leaks.
func (p *Proxy) WatchJobs(stream schedulerv1pb.Scheduler_WatchJobsServer) error {
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

	upstream, err := p.client.WatchJobs(ctx)
	if err != nil {
		return err
	}

	errCh := make(chan error, 2)

	go func() {
		for {
			msg, rerr := stream.Recv()
			if rerr != nil {
				errCh <- rerr
				return
			}
			if serr := upstream.Send(msg); serr != nil {
				errCh <- serr
				return
			}
		}
	}()

	go func() {
		for {
			msg, rerr := upstream.Recv()
			if rerr != nil {
				errCh <- rerr
				return
			}
			if serr := stream.Send(msg); serr != nil {
				errCh <- serr
				return
			}
		}
	}()

	first := <-errCh
	cancel()
	<-errCh
	if errors.Is(first, io.EOF) {
		return nil
	}
	return first
}

// WatchHosts forwards host updates from the upstream scheduler, rewriting
// every Host.Address so daprd reconnects to the proxy rather than to the
// real scheduler on every refresh. Without this rewrite daprd would bypass
// the proxy as soon as the first host list arrived.
func (p *Proxy) WatchHosts(req *schedulerv1pb.WatchHostsRequest, stream schedulerv1pb.Scheduler_WatchHostsServer) error {
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

	upstream, err := p.client.WatchHosts(ctx, req)
	if err != nil {
		return err
	}

	for {
		msg, rerr := upstream.Recv()
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return nil
			}
			return rerr
		}
		for _, h := range msg.GetHosts() {
			h.Address = p.Address()
		}
		if serr := stream.Send(msg); serr != nil {
			return serr
		}
	}
}
