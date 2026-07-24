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
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
)

// sentinelUpstream returns a distinctive status from GetJob so a forwarded
// call cannot be confused with a generic Unimplemented from a broken proxy.
type sentinelUpstream struct {
	schedulerv1pb.UnimplementedSchedulerServer
}

func (sentinelUpstream) GetJob(context.Context, *schedulerv1pb.GetJobRequest) (*schedulerv1pb.GetJobResponse, error) {
	return nil, status.Error(codes.AlreadyExists, "sentinel upstream")
}

func TestProxyServesOnReservedListener(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	// Fake healthz endpoint so Scheduler.WaitUntilRunning succeeds without
	// running the scheduler binary.
	healthLis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	healthPort := healthLis.Addr().(*net.TCPAddr).Port
	healthSrv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
		ReadHeaderTimeout: time.Second * 5,
	}
	healthServeErr := make(chan error, 1)
	go func() { healthServeErr <- healthSrv.Serve(healthLis) }()
	defer func() {
		require.NoError(t, healthSrv.Close())
		if serr := <-healthServeErr; !errors.Is(serr, http.ErrServerClosed) {
			require.NoError(t, serr)
		}
	}()

	upstreamLis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	upstreamPort := upstreamLis.Addr().(*net.TCPAddr).Port
	upstreamSrv := grpc.NewServer()
	schedulerv1pb.RegisterSchedulerServer(upstreamSrv, sentinelUpstream{})
	upstreamServeErr := make(chan error, 1)
	go func() { upstreamServeErr <- upstreamSrv.Serve(upstreamLis) }()
	defer func() {
		upstreamSrv.Stop()
		if serr := <-upstreamServeErr; serr != nil && !errors.Is(serr, grpc.ErrServerStopped) {
			require.NoError(t, serr)
		}
	}()

	// WithEmbed(false) skips the etcd leadership check in WaitUntilRunning.
	sched := scheduler.New(t,
		scheduler.WithEmbed(false),
		scheduler.WithHealthzPort(healthPort),
		scheduler.WithPort(upstreamPort),
	)

	p := New(t, sched)
	// Registered after ports.Reserve's cleanup inside New, so it runs first.
	t.Cleanup(func() { p.Cleanup(t) })
	require.NotNil(t, p.listener, "New must bind and retain the reserved listener")
	tcp, ok := p.listener.Addr().(*net.TCPAddr)
	require.True(t, ok)
	require.Equal(t, tcp.Port, p.Port(), "advertised port must match the retained listener address")
	listenerBefore := p.listener

	p.Run(t, ctx)
	require.Same(t, listenerBefore, p.listener, "Run must serve on the listener reserved in New, not re-bind the port")

	conn, err := grpc.NewClient(p.Address(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	// Close the client before proxy GracefulStop so it has no live connections.
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	require.True(t, waitReady(ctx, conn),
		"proxy connection never became ready: address=%s state=%s ctx err=%v",
		p.Address(), conn.GetState(), ctx.Err())

	_, err = schedulerv1pb.NewSchedulerClient(conn).GetJob(ctx, new(schedulerv1pb.GetJobRequest))
	st, ok := status.FromError(err)
	require.True(t, ok, "expected a gRPC status error, got: %v", err)
	require.Equal(t, codes.AlreadyExists, st.Code(), "expected forwarded RPC to reach the sentinel upstream: %v", err)
	require.Equal(t, "sentinel upstream", st.Message())
}
