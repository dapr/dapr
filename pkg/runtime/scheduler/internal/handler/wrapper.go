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

package handler

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

type wrapper struct {
	h *Handler
}

func (w *wrapper) DeleteJob(ctx context.Context, req *v1pb.DeleteJobRequest, opts ...grpc.CallOption) (*v1pb.DeleteJobResponse, error) {
	var resp *v1pb.DeleteJobResponse
	err := w.call(ctx, func(client v1pb.SchedulerClient) error {
		var err error
		resp, err = client.DeleteJob(ctx, req, opts...)
		return err
	})

	return resp, err
}

func (w *wrapper) GetJob(ctx context.Context, req *v1pb.GetJobRequest, opts ...grpc.CallOption) (*v1pb.GetJobResponse, error) {
	var resp *v1pb.GetJobResponse
	err := w.call(ctx, func(client v1pb.SchedulerClient) error {
		var err error
		resp, err = client.GetJob(ctx, req, opts...)
		return err
	})
	return resp, err
}

func (w *wrapper) ListJobs(ctx context.Context, req *v1pb.ListJobsRequest, opts ...grpc.CallOption) (*v1pb.ListJobsResponse, error) {
	var resp *v1pb.ListJobsResponse
	err := w.call(ctx, func(client v1pb.SchedulerClient) error {
		var err error
		resp, err = client.ListJobs(ctx, req, opts...)
		return err
	})
	return resp, err
}

func (w *wrapper) ScheduleJob(ctx context.Context, req *v1pb.ScheduleJobRequest, opts ...grpc.CallOption) (*v1pb.ScheduleJobResponse, error) {
	var resp *v1pb.ScheduleJobResponse
	err := w.call(ctx, func(client v1pb.SchedulerClient) error {
		var err error
		resp, err = client.ScheduleJob(ctx, req, opts...)
		return err
	})
	return resp, err
}

func (w *wrapper) WatchJobs(ctx context.Context, opts ...grpc.CallOption) (v1pb.Scheduler_WatchJobsClient, error) {
	var resp v1pb.Scheduler_WatchJobsClient
	err := w.call(ctx, func(client v1pb.SchedulerClient) error {
		var err error
		resp, err = client.WatchJobs(ctx, opts...)
		return err
	})
	return resp, err
}

func (w *wrapper) WatchHosts(ctx context.Context, req *v1pb.WatchHostsRequest, opts ...grpc.CallOption) (v1pb.Scheduler_WatchHostsClient, error) {
	var resp v1pb.Scheduler_WatchHostsClient
	err := w.call(ctx, func(client v1pb.SchedulerClient) error {
		var err error
		resp, err = client.WatchHosts(ctx, req, opts...)
		return err
	})
	return resp, err
}

func (w *wrapper) Addresses() []string {
	w.h.lock.RLock()
	defer w.h.lock.RUnlock()
	return w.h.broadcastAddresses
}

type apiFn func(client v1pb.SchedulerClient) error

func (w *wrapper) call(ctx context.Context, fn apiFn) error {
	if w.h.NotEnabled() {
		return errors.New("scheduler not enabled")
	}

	for {
		client, err := w.h.next(ctx)
		if err != nil {
			return err
		}

		err = fn(client)
		status, ok := status.FromError(err)
		if ok && status.Code() == codes.Canceled {
			continue
		}

		return err
	}
}
