// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"context"
	"fmt"
	"log"
	"net"

	pb "github.com/dapr/dapr/pkg/proto/daprclient"
	grpc "google.golang.org/grpc"

	"github.com/golang/protobuf/ptypes/any"
	"github.com/golang/protobuf/ptypes/empty"
)

// server is our user app
type server struct {
}

// Sample method to invoke
func (s *server) MyMethod() string {
	return "Hi there!"
}

// This method gets invoked when a remote service has called the app through Dapr
// The payload carries a Method to identify the method, a set of metadata properties and an optional payload
func (s *server) OnInvoke(ctx context.Context, in *pb.InvokeEnvelope) (*any.Any, error) {
	var response string
	switch in.Method {
	case "MyMethod":
		response = s.MyMethod()
	}
	return &any.Any{
		Value: []byte(response),
	}, nil
}

// Dapr will call this method to get the list of topics the app wants to subscribe to. In this example, we are telling Dapr
// To subscribe to a topic named TopicA
func (s *server) GetTopicSubscriptions(ctx context.Context, in *empty.Empty) (*pb.GetTopicSubscriptionsEnvelope, error) {
	return &pb.GetTopicSubscriptionsEnvelope{
		Topics: []string{"TopicA"},
	}, nil
}

// Dapr will call this method to get the list of bindings the app will get invoked by. In this example, we are telling Dapr
// To invoke our app with a binding named storage
func (s *server) GetBindingsSubscriptions(ctx context.Context, in *empty.Empty) (*pb.GetBindingsSubscriptionsEnvelope, error) {
	return &pb.GetBindingsSubscriptionsEnvelope{
		Bindings: []string{"storage"},
	}, nil
}

// This method gets invoked every time a new event is fired from a registered binding. The message carries the binding name, a payload and optional metadata
func (s *server) OnBindingEvent(ctx context.Context, in *pb.BindingEventEnvelope) (*pb.BindingResponseEnvelope, error) {
	fmt.Println("Invoked from binding")
	return &pb.BindingResponseEnvelope{}, nil
}

// This method is fired whenever a message has been published to a topic that has been subscribed. Dapr sends published messages in a CloudEvents 0.3 envelope.
func (s *server) OnTopicEvent(ctx context.Context, in *pb.CloudEventEnvelope) (*empty.Empty, error) {
	fmt.Println("Topic message arrived")
	return &empty.Empty{}, nil
}

func main() {
	// create listener
	lis, err := net.Listen("tcp", ":4000")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	// create grpc server
	s := grpc.NewServer()
	pb.RegisterDaprClientServer(s, &server{})

	fmt.Println("Client starting...")

	// and start...
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
