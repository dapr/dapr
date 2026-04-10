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

package fake

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

// Client is a fake implementation of schedulerv1pb.SchedulerClient for unit
// testing. Only the WatchJobs method is faked; all other methods panic.
type Client struct {
	schedulerv1pb.SchedulerClient
	fnWatchJobs func(ctx context.Context, opts ...grpc.CallOption) (schedulerv1pb.Scheduler_WatchJobsClient, error)
}

func NewClient() *Client {
	return &Client{
		fnWatchJobs: func(ctx context.Context, opts ...grpc.CallOption) (schedulerv1pb.Scheduler_WatchJobsClient, error) {
			return NewStream().WithContext(ctx), nil
		},
	}
}

func (c *Client) WithWatchJobs(fn func(ctx context.Context, opts ...grpc.CallOption) (schedulerv1pb.Scheduler_WatchJobsClient, error)) *Client {
	c.fnWatchJobs = fn
	return c
}

func (c *Client) WatchJobs(ctx context.Context, opts ...grpc.CallOption) (schedulerv1pb.Scheduler_WatchJobsClient, error) {
	return c.fnWatchJobs(ctx, opts...)
}

// Stream is a fake implementation of schedulerv1pb.Scheduler_WatchJobsClient
// for unit testing.
type Stream struct {
	fnSend func(*schedulerv1pb.WatchJobsRequest) error
	fnRecv func() (*schedulerv1pb.WatchJobsResponse, error)
	ctx    context.Context
}

func NewStream() *Stream {
	return &Stream{
		fnSend: func(*schedulerv1pb.WatchJobsRequest) error { return nil },
		fnRecv: nil,
		ctx:    context.Background(),
	}
}

func (s *Stream) WithSend(fn func(*schedulerv1pb.WatchJobsRequest) error) *Stream {
	s.fnSend = fn
	return s
}

func (s *Stream) WithRecv(fn func() (*schedulerv1pb.WatchJobsResponse, error)) *Stream {
	s.fnRecv = fn
	return s
}

func (s *Stream) WithContext(ctx context.Context) *Stream {
	s.ctx = ctx
	return s
}

func (s *Stream) Send(req *schedulerv1pb.WatchJobsRequest) error {
	return s.fnSend(req)
}

func (s *Stream) Recv() (*schedulerv1pb.WatchJobsResponse, error) {
	if s.fnRecv != nil {
		return s.fnRecv()
	}
	<-s.ctx.Done()
	return nil, s.ctx.Err()
}

func (s *Stream) Header() (metadata.MD, error) { return nil, nil }
func (s *Stream) Trailer() metadata.MD         { return nil }
func (s *Stream) CloseSend() error             { return nil }
func (s *Stream) Context() context.Context     { return s.ctx }
func (s *Stream) SendMsg(any) error            { return nil }
func (s *Stream) RecvMsg(any) error            { return nil }
