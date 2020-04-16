// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"encoding/json"
	"fmt"
	"log"

	"context"

	"net"

	"github.com/golang/protobuf/ptypes/any"
	"github.com/golang/protobuf/ptypes/empty"

	pb "github.com/dapr/dapr/pkg/proto/daprclient"
	"google.golang.org/grpc"
)

// server is our user app
type server struct {
}

type appResponse struct {
	Message string `json:"message,omitempty"`
}

func main() {
	log.Printf("Initializing grpc")

	/* #nosec */
	lis, err := net.Listen("tcp", ":3000")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	/* #nosec */
	s := grpc.NewServer()
	pb.RegisterDaprClientServer(s, &server{})

	fmt.Println("Client starting...")

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

// This is the server side in a grpc -> grpc test.  It responds with the same string it was sent.
func (s *server) grpcTestHandler(data []byte) ([]byte, error) {
	var t string
	err := json.Unmarshal(data, &t)
	if err != nil {
		return nil, err
	}

	fmt.Printf("received %s\n", t)
	resp, err := json.Marshal(appResponse{Message: t})
	if err != nil {
		fmt.Println("not marshal")
	}

	return resp, err
}

// This method gets invoked when a remote service has called the app through Dapr
// The payload carries a Method to identify the method, a set of metadata properties and an optional payload
func (s *server) OnInvoke(ctx context.Context, in *pb.InvokeEnvelope) (*any.Any, error) {
	fmt.Printf("Got invoked method %s and data: %s\n", in.Method, string(in.Data.Value))

	var err error
	var response []byte
	switch in.Method {
	case "httpToGrpcTest":
		// not a typo, the handling is the same as the case below
		fallthrough
	case "grpcToGrpcTest":
		response, err = s.grpcTestHandler(in.Data.Value)
	}

	if err != nil {
		msg := "Error: " + err.Error()
		response, _ = json.Marshal(msg)
	}

	return &any.Any{
		Value: response,
	}, nil
}

// Dapr will call this method to get the list of topics the app wants to subscribe to. In this example, we are telling Dapr
// To subscribe to a topic named TopicA
func (s *server) GetTopicSubscriptions(ctx context.Context, in *empty.Empty) (*pb.GetTopicSubscriptionsEnvelope, error) {
	return &pb.GetTopicSubscriptionsEnvelope{
		Topics: []string{"TopicA"},
	}, nil
}

// Dapper will call this method to get the list of bindings the app will get invoked by. In this example, we are telling Dapr
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
