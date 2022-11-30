/*
Copyright 2022 The Dapr Authors
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

package server

import (
	"errors"
	"net"
	"sync/atomic"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	apppb "github.com/dapr/dapr/tests/apps/perf/pubsub_subscribe_grpc/pkg/proto"

	"google.golang.org/grpc"
)

const defaultChannelSize = 1000

// Server is the gRPC service implementation for Dapr.
type Server struct {
	apppb.UnimplementedPerfTestNotifierServer
	runtimev1pb.UnimplementedAppCallbackHealthCheckServer
	runtimev1pb.UnimplementedDaprServer
	listener                   net.Listener
	grpcServer                 *grpc.Server
	started                    uint32
	onTopicEventNotifyChan     chan struct{}
	onBulkTopicEventNotifyChan chan int
	subscriptions              map[string]*runtimev1pb.TopicSubscription
}

func NewService(address string) (*Server, error) {
	lis, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}

	s := &Server{
		listener:                   lis,
		subscriptions:              map[string]*runtimev1pb.TopicSubscription{},
		onTopicEventNotifyChan:     make(chan struct{}, defaultChannelSize),
		onBulkTopicEventNotifyChan: make(chan int, defaultChannelSize),
	}
	gs := grpc.NewServer()

	runtimev1pb.RegisterAppCallbackServer(gs, s)
	runtimev1pb.RegisterAppCallbackHealthCheckServer(gs, s)
	runtimev1pb.RegisterAppCallbackAlphaServer(gs, s)
	apppb.RegisterPerfTestNotifierServer(gs, s)

	s.grpcServer = gs
	return s, nil
}

func (s *Server) Start() error {
	if !atomic.CompareAndSwapUint32(&s.started, 0, 1) {
		return errors.New("a gRPC server can only be started once")
	}
	return s.grpcServer.Serve(s.listener)
}

func (s *Server) Stop() error {
	if atomic.LoadUint32(&s.started) == 0 {
		return nil
	}
	s.grpcServer.Stop()
	s.grpcServer = nil
	return nil
}

// Subscribe implements proto.PerfTestNotifierServer
func (s *Server) Subscribe(r *apppb.Request, a apppb.PerfTestNotifier_SubscribeServer) error {
	count := 0
	bulkCount := 0
	for {
		select {
		case <-s.onTopicEventNotifyChan:
			count++
			// If N messages are received, test is completed, notify the client.
			if count >= int(r.NumMessages) {
				if err := a.Send(&apppb.Response{}); err != nil {
					return err
				}
				count = 0
			}
		case _bulkCount := <-s.onBulkTopicEventNotifyChan:
			bulkCount += _bulkCount
			// If N messages are received, test is completed, notify the client.
			if bulkCount >= int(r.NumMessages) {
				if err := a.Send(&apppb.Response{}); err != nil {
					return err
				}
				bulkCount = 0
			}
		case <-a.Context().Done():
			return nil
		}
	}
}
