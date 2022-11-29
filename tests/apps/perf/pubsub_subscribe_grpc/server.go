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

package main

import (
	"errors"
	"net"
	"sync/atomic"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"google.golang.org/grpc"
)

// Server is the gRPC service implementation for Dapr.
type Server struct {
	runtimev1pb.UnimplementedAppCallbackServer
	runtimev1pb.UnimplementedAppCallbackHealthCheckServer
	runtimev1pb.UnimplementedAppCallbackAlphaServer
	runtimev1pb.UnimplementedDaprServer
	listener      net.Listener
	grpcServer    *grpc.Server
	started       uint32
	subscriptions map[string]*runtimev1pb.TopicSubscription
}

func newService(address string) (*Server, error) {
	lis, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}

	s := &Server{
		listener:      lis,
		subscriptions: map[string]*runtimev1pb.TopicSubscription{},
	}
	gs := grpc.NewServer()

	runtimev1pb.RegisterAppCallbackServer(gs, s)
	runtimev1pb.RegisterAppCallbackHealthCheckServer(gs, s)
	runtimev1pb.RegisterAppCallbackAlphaServer(gs, s)

	s.grpcServer = gs
	return s, nil
}

func (s *Server) start() error {
	if !atomic.CompareAndSwapUint32(&s.started, 0, 1) {
		return errors.New("a gRPC server can only be started once")
	}
	return s.grpcServer.Serve(s.listener)
}

func (s *Server) stop() error {
	if atomic.LoadUint32(&s.started) == 0 {
		return nil
	}
	s.grpcServer.Stop()
	s.grpcServer = nil
	return nil
}
