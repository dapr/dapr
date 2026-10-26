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

package watchjobs

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(deadstream))
}

// deadstream verifies that an abruptly dead WatchJobs stream cannot take down
// its namespace. Job payloads larger than the per-stream flow-control window
// are sent to a client that never reads, parking the server's Send; when that
// client dies, the parked send and every queued trigger fail at once. The
// resulting burst of close events for the one stream must dedupe: duplicate
// events double-decrement the namespace connection count, deleting the
// namespace while live streams remain and dropping every stream and
// deliverable prefix in it. The failed triggers must also resolve promptly as
// undeliverable so the cron engine redelivers them to the surviving stream.
type deadstream struct {
	scheduler *scheduler.Scheduler
}

func (d *deadstream) Setup(t *testing.T) []framework.Option {
	d.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(d.scheduler),
	}
}

func (d *deadstream) Run(t *testing.T, ctx context.Context) {
	d.scheduler.WaitUntilRunning(t, ctx)

	// Live client: reads and ACKs every job.
	live := d.scheduler.WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId: "appid", Namespace: "default",
	})

	// Doomed client: connects, registers, then never reads.
	conn, err := grpc.NewClient(d.scheduler.Address(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	doomedCtx, doomedCancel := context.WithCancel(ctx)
	t.Cleanup(doomedCancel)
	doomed, err := schedulerv1pb.NewSchedulerClient(conn).WatchJobs(doomedCtx)
	require.NoError(t, err)
	require.NoError(t, doomed.Send(&schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{
			Initial: &schedulerv1pb.WatchJobsRequestInitial{
				AppId: "appid", Namespace: "default",
			},
		},
	}))

	d.scheduler.WaitUntilSidecarsConnected(t, ctx, 2)

	// Payloads larger than the default 64KiB per-stream flow-control window:
	// the doomed stream's first Send parks, and the round-robin half of the
	// remaining triggers queue up behind it.
	payload, err := anypb.New(wrapperspb.Bytes(make([]byte, 128*1024)))
	require.NoError(t, err)

	const jobs = 20
	client := d.scheduler.Client(t, ctx)
	for i := range jobs {
		_, err = client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
			Name: fmt.Sprintf("fat-%d", i),
			Job: &schedulerv1pb.Job{
				DueTime: new(time.Now().Format(time.RFC3339)),
				Data:    payload,
			},
			Metadata: &schedulerv1pb.JobMetadata{
				AppId: "appid", Namespace: "default",
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Job{
						Job: new(schedulerv1pb.TargetJob),
					},
				},
			},
		})
		require.NoError(t, err)
	}

	seen := make(map[string]bool)
	drain := func(bound time.Duration, want int) {
		timeout := time.After(bound)
		for len(seen) < want {
			select {
			case name := <-live:
				seen[name] = true
			case <-timeout:
				return
			}
		}
	}

	// The live stream receives its round-robin half while the doomed stream
	// blocks the rest.
	drain(15*time.Second, jobs/2)
	require.GreaterOrEqual(t, len(seen), jobs/2,
		"live stream did not receive its share of jobs: got %d", len(seen))

	// Kill the doomed client: the parked send and all queued triggers fail.
	// Every one of their jobs must be redelivered to the live stream, which
	// must itself survive.
	doomedCancel()

	drain(15*time.Second, jobs)
	require.Len(t, seen, jobs,
		"jobs routed to the dead stream were not redelivered to the live stream")

	// The namespace must still be intact: exactly one connected sidecar and a
	// fresh job still delivers.
	d.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)

	_, err = client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name: "final",
		Job: &schedulerv1pb.Job{
			DueTime: new(time.Now().Format(time.RFC3339)),
		},
		Metadata: &schedulerv1pb.JobMetadata{
			AppId: "appid", Namespace: "default",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{
					Job: new(schedulerv1pb.TargetJob),
				},
			},
		},
	})
	require.NoError(t, err)

	select {
	case name := <-live:
		require.Equal(t, "final", name)
	case <-time.After(10 * time.Second):
		t.Fatal("job scheduled after dead-stream cleanup was not delivered: namespace routing lost")
	}
}
