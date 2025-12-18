/*
Copyright 2025 The Dapr Authors
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

package fake

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

type Fake struct {
	client schedulerv1pb.SchedulerClient

	scheduleJobFn        func(context.Context, *schedulerv1pb.ScheduleJobRequest) (*schedulerv1pb.ScheduleJobResponse, error)
	deleteJobFn          func(context.Context, *schedulerv1pb.DeleteJobRequest) (*schedulerv1pb.DeleteJobResponse, error)
	getJobFn             func(context.Context, *schedulerv1pb.GetJobRequest) (*schedulerv1pb.GetJobResponse, error)
	listJobsFn           func(context.Context, *schedulerv1pb.ListJobsRequest) (*schedulerv1pb.ListJobsResponse, error)
	watchHostsFn         func(*schedulerv1pb.WatchHostsRequest, schedulerv1pb.Scheduler_WatchHostsServer) error
	watchJobsFn          func(schedulerv1pb.Scheduler_WatchJobsServer) error
	deleteByMetadataFn   func(ctx context.Context, req *schedulerv1pb.DeleteByMetadataRequest) (*schedulerv1pb.DeleteByMetadataResponse, error)
	deleteByNamePrefixFn func(ctx context.Context, req *schedulerv1pb.DeleteByNamePrefixRequest) (*schedulerv1pb.DeleteByNamePrefixResponse, error)
}

func New(t *testing.T) *Fake {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	f := &Fake{
		scheduleJobFn: func(context.Context, *schedulerv1pb.ScheduleJobRequest) (*schedulerv1pb.ScheduleJobResponse, error) {
			return nil, nil
		},
		deleteJobFn: func(context.Context, *schedulerv1pb.DeleteJobRequest) (*schedulerv1pb.DeleteJobResponse, error) {
			return nil, nil
		},
		getJobFn: func(context.Context, *schedulerv1pb.GetJobRequest) (*schedulerv1pb.GetJobResponse, error) {
			return nil, nil
		},
		listJobsFn: func(context.Context, *schedulerv1pb.ListJobsRequest) (*schedulerv1pb.ListJobsResponse, error) {
			return nil, nil
		},
		watchHostsFn: func(*schedulerv1pb.WatchHostsRequest, schedulerv1pb.Scheduler_WatchHostsServer) error {
			return nil
		},
		watchJobsFn: func(schedulerv1pb.Scheduler_WatchJobsServer) error {
			return nil
		},
		deleteByMetadataFn: func(ctx context.Context, req *schedulerv1pb.DeleteByMetadataRequest) (*schedulerv1pb.DeleteByMetadataResponse, error) {
			return nil, nil
		},
		deleteByNamePrefixFn: func(ctx context.Context, req *schedulerv1pb.DeleteByNamePrefixRequest) (*schedulerv1pb.DeleteByNamePrefixResponse, error) {
			return nil, nil
		},
	}

	server := grpc.NewServer()
	schedulerv1pb.RegisterSchedulerServer(server, f)

	errCh := make(chan error)
	go func() {
		errCh <- server.Serve(lis)
	}()

	t.Cleanup(func() {
		server.GracefulStop()
		select {
		case <-time.After(time.Second * 5):
			require.Fail(t, "timeout waiting for server to stop")
		case err = <-errCh:
			require.NoError(t, err)
		}
	})

	client, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	f.client = schedulerv1pb.NewSchedulerClient(client)

	return f
}

func (f *Fake) Client() schedulerv1pb.SchedulerClient {
	return f.client
}

func (f *Fake) WithScheduleJob(fn func(context.Context, *schedulerv1pb.ScheduleJobRequest) (*schedulerv1pb.ScheduleJobResponse, error)) *Fake {
	f.scheduleJobFn = fn
	return f
}

func (f *Fake) WithDeleteJob(fn func(context.Context, *schedulerv1pb.DeleteJobRequest) (*schedulerv1pb.DeleteJobResponse, error)) *Fake {
	f.deleteJobFn = fn
	return f
}

func (f *Fake) WithGetJob(fn func(context.Context, *schedulerv1pb.GetJobRequest) (*schedulerv1pb.GetJobResponse, error)) *Fake {
	f.getJobFn = fn
	return f
}

func (f *Fake) WithListJobs(fn func(context.Context, *schedulerv1pb.ListJobsRequest) (*schedulerv1pb.ListJobsResponse, error)) *Fake {
	f.listJobsFn = fn
	return f
}

func (f *Fake) WithWatchHosts(fn func(*schedulerv1pb.WatchHostsRequest, schedulerv1pb.Scheduler_WatchHostsServer) error) *Fake {
	f.watchHostsFn = fn
	return f
}

func (f *Fake) WithWatchJobs(fn func(schedulerv1pb.Scheduler_WatchJobsServer) error) *Fake {
	f.watchJobsFn = fn
	return f
}

func (f *Fake) WithDeleteByMetadata(fn func(ctx context.Context, req *schedulerv1pb.DeleteByMetadataRequest) (*schedulerv1pb.DeleteByMetadataResponse, error)) *Fake {
	f.deleteByMetadataFn = fn
	return f
}

func (f *Fake) WithDeleteByNamePrefix(fn func(ctx context.Context, req *schedulerv1pb.DeleteByNamePrefixRequest) (*schedulerv1pb.DeleteByNamePrefixResponse, error)) *Fake {
	f.deleteByNamePrefixFn = fn
	return f
}

func (f *Fake) ScheduleJob(ctx context.Context, req *schedulerv1pb.ScheduleJobRequest) (*schedulerv1pb.ScheduleJobResponse, error) {
	return f.scheduleJobFn(ctx, req)
}

func (f *Fake) DeleteJob(ctx context.Context, req *schedulerv1pb.DeleteJobRequest) (*schedulerv1pb.DeleteJobResponse, error) {
	return f.deleteJobFn(ctx, req)
}

func (f *Fake) GetJob(ctx context.Context, req *schedulerv1pb.GetJobRequest) (*schedulerv1pb.GetJobResponse, error) {
	return f.getJobFn(ctx, req)
}

func (f *Fake) ListJobs(ctx context.Context, req *schedulerv1pb.ListJobsRequest) (*schedulerv1pb.ListJobsResponse, error) {
	return f.listJobsFn(ctx, req)
}

func (f *Fake) WatchHosts(req *schedulerv1pb.WatchHostsRequest, stream schedulerv1pb.Scheduler_WatchHostsServer) error {
	return f.watchHostsFn(req, stream)
}

func (f *Fake) WatchJobs(stream schedulerv1pb.Scheduler_WatchJobsServer) error {
	return f.watchJobsFn(stream)
}

func (f *Fake) DeleteByMetadata(ctx context.Context, req *schedulerv1pb.DeleteByMetadataRequest) (*schedulerv1pb.DeleteByMetadataResponse, error) {
	return f.deleteByMetadataFn(ctx, req)
}

func (f *Fake) DeleteByNamePrefix(ctx context.Context, req *schedulerv1pb.DeleteByNamePrefixRequest) (*schedulerv1pb.DeleteByNamePrefixResponse, error) {
	return f.deleteByNamePrefixFn(ctx, req)
}
