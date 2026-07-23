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

package shutdown

import (
	"context"
	"runtime"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(stalledwatch))
}

// stalledwatch verifies the scheduler exits within a bounded time on shutdown
// even when a WatchJobs stream is parked in its initial Recv. Such a handler
// never reaches its closeCh select, so an unbounded GracefulStop waits on it
// forever: the process becomes a zombie that ACKs gRPC keepalives and serves
// healthz 200 while every handler is dead, turning an engine failure into a
// cluster-wide outage window.
type stalledwatch struct {
	scheduler *scheduler.Scheduler
}

func (s *stalledwatch) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on windows which relies on unix process signals")
	}

	s.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(),
	}
}

func (s *stalledwatch) Run(t *testing.T, ctx context.Context) {
	s.scheduler.Run(t, ctx)
	s.scheduler.WaitUntilRunning(t, ctx)

	// Open a WatchJobs stream but never send the initial request: the server
	// handler parks in its first Recv. Keep the connection alive for the
	// whole shutdown so the transport keeps ACKing keepalives.
	conn, err := grpc.NewClient(s.scheduler.Address(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	streamCtx, streamCancel := context.WithCancel(ctx)
	t.Cleanup(streamCancel)
	stream, err := schedulerv1pb.NewSchedulerClient(conn).WatchJobs(streamCtx)
	if err != nil {
		t.Fatal(err)
	}
	_ = stream

	// Give the RPC time to reach the server and park in Recv.
	time.Sleep(time.Second)

	done := make(chan struct{})
	go func() {
		s.scheduler.Cleanup(t)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second * 15):
		t.Fatal("scheduler did not exit within 15s of SIGINT while a WatchJobs stream was parked in its initial Recv")
	}
}
