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

package api

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(triggerstall))
}

// triggerstall verifies that the scheduler cluster recovers from a quorum
// change when triggers are pending and no new gRPC connections are opened.
type triggerstall struct {
	scheduler1 *scheduler.Scheduler
	scheduler2 *scheduler.Scheduler
	scheduler3 *scheduler.Scheduler
	scheduler4 *scheduler.Scheduler
}

func (s *triggerstall) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Cleanup does not work cleanly on windows")
	}

	fp := ports.Reserve(t, 6)
	port1, port2, port3 := fp.Port(t), fp.Port(t), fp.Port(t)
	port4, port5, port6 := fp.Port(t), fp.Port(t), fp.Port(t)

	clusterOpts := []scheduler.Option{
		scheduler.WithInitialCluster(fmt.Sprintf(
			"scheduler-0=http://127.0.0.1:%d,scheduler-1=http://127.0.0.1:%d,scheduler-2=http://127.0.0.1:%d",
			port1, port2, port3),
		),
	}

	s.scheduler1 = scheduler.New(t, append(clusterOpts,
		scheduler.WithID("scheduler-0"),
		scheduler.WithEtcdClientPort(port4),
	)...)
	s.scheduler2 = scheduler.New(t, append(clusterOpts,
		scheduler.WithID("scheduler-1"),
		scheduler.WithEtcdClientPort(port5),
	)...)
	s.scheduler3 = scheduler.New(t, append(clusterOpts,
		scheduler.WithID("scheduler-2"),
		scheduler.WithEtcdClientPort(port6),
	)...)

	s.scheduler4 = scheduler.New(t,
		scheduler.WithID(s.scheduler3.ID()),
		scheduler.WithEtcdClientPort(port6),
		scheduler.WithInitialCluster(s.scheduler3.InitialCluster()),
		scheduler.WithDataDir(s.scheduler3.DataDir()),
		scheduler.WithPort(s.scheduler3.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(fp, s.scheduler1, s.scheduler2, s.scheduler3),
	}
}

func (s *triggerstall) Run(t *testing.T, ctx context.Context) {
	s.scheduler1.WaitUntilRunning(t, ctx)
	s.scheduler2.WaitUntilRunning(t, ctx)
	s.scheduler3.WaitUntilRunning(t, ctx)

	watchCtx, watchCancel := context.WithCancel(context.Background())
	t.Cleanup(watchCancel)

	triggerCh := make(chan *schedulerv1.WatchJobsResponse, 100)

	for _, sched := range []*scheduler.Scheduler{s.scheduler1, s.scheduler2, s.scheduler3} {
		//nolint:staticcheck
		conn, err := grpc.DialContext(watchCtx, sched.Address(),
			grpc.WithDefaultCallOptions(
				grpc.MaxCallSendMsgSize(math.MaxInt32),
				grpc.MaxCallRecvMsgSize(math.MaxInt32),
			),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(), grpc.WithReturnConnectionError(),
		)
		require.NoError(t, err)
		t.Cleanup(func() { conn.Close() })

		w, err := schedulerv1.NewSchedulerClient(conn).WatchJobs(watchCtx)
		require.NoError(t, err)
		require.NoError(t, w.Send(&schedulerv1.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1.WatchJobsRequest_Initial{
				Initial: &schedulerv1.WatchJobsRequestInitial{
					AppId: "testapp", Namespace: "default",
				},
			},
		}))

		go func(w schedulerv1.Scheduler_WatchJobsClient) {
			for {
				resp, err := w.Recv()
				if err != nil {
					return
				}
				select {
				case triggerCh <- resp:
				case <-ctx.Done():
					return
				}
			}
		}(w)
	}

	client := s.scheduler1.Client(t, ctx)
	for i := range 10 {
		_, err := client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
			Name: fmt.Sprintf("job-%d", i),
			Job: &schedulerv1.Job{
				Schedule: new("@every 2s"),
				DueTime:  new(time.Now().Format(time.RFC3339)),
			},
			Metadata: &schedulerv1.JobMetadata{
				AppId: "testapp", Namespace: "default",
				Target: &schedulerv1.JobTargetMetadata{
					Type: &schedulerv1.JobTargetMetadata_Job{
						Job: new(schedulerv1.TargetJob),
					},
				},
			},
		})
		require.NoError(t, err)
	}

	for range 10 {
		select {
		case job := <-triggerCh:
			t.Logf("Stalled: %s", job.GetName())
		case <-time.After(10 * time.Second):
			require.Fail(t, "Timed out waiting for initial trigger")
		}
	}

	s.scheduler3.Kill(t)
	s.scheduler4.Run(t, ctx)
	t.Cleanup(func() { s.scheduler4.Kill(t) })
	s.scheduler4.WaitUntilRunning(t, ctx)

	select {
	case job := <-triggerCh:
		t.Logf("Trigger after recovery: %s", job.GetName())
	case <-time.After(25 * time.Second):
		t.Logf("Leadership keys: %v",
			s.scheduler1.ListAllKeys(t, ctx, "dapr/leadership"))
		require.Fail(t, "No triggers after quorum change within 25s")
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		keys := s.scheduler1.ListAllKeys(t, ctx, "dapr/leadership")
		assert.Len(c, keys, 3, "expected 3 leadership keys, got: %v", keys)
	}, 10*time.Second, 10*time.Millisecond)
}
