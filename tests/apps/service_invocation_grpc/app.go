/*
Copyright 2021 The Dapr Authors
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
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// server is our user app
type server struct{}

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
	pb.RegisterAppCallbackServer(s, &server{})

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

func (s *server) retrieveRequestObject(ctx context.Context) ([]byte, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	requestMD := map[string][]string{}
	for k, vals := range md {
		requestMD[k] = vals
		fmt.Printf("incoming md: %s %q", k, vals)
	}

	header := metadata.Pairs(
		"DaprTest-Response-1", "DaprTest-Response-Value-1",
		"DaprTest-Response-2", "DaprTest-Response-Value-2")

	// following traceid byte is of expectedTraceID "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	scConfig := trace.SpanContextConfig{
		TraceID:    trace.TraceID{75, 249, 47, 53, 119, 179, 77, 166, 163, 206, 146, 157, 14, 14, 71, 54},
		SpanID:     trace.SpanID{0, 240, 103, 170, 11, 169, 2, 183},
		TraceFlags: trace.TraceFlags(1),
	}
	sc := trace.NewSpanContext(scConfig)
	header.Set("grpc-trace-bin", string(diag_utils.BinaryFromSpanContext(sc)))

	grpc.SendHeader(ctx, header)
	trailer := metadata.Pairs(
		"DaprTest-Trailer-1", "DaprTest-Trailer-Value-1",
		"DaprTest-Trailer-2", "DaprTest-Trailer-Value-2")
	grpc.SetTrailer(ctx, trailer)

	return json.Marshal(requestMD)
}

// OnInvoke This method gets invoked when a remote service has called the app through Dapr
// The payload carries a Method to identify the method, a set of metadata properties and an optional payload
func (s *server) OnInvoke(ctx context.Context, in *commonv1pb.InvokeRequest) (*commonv1pb.InvokeResponse, error) {
	fmt.Printf("Got invoked method %s and data: %s\n", in.Method, string(in.GetData().Value))

	var err error
	var response []byte
	switch in.Method {
	case "httpToGrpcTest":
		// not a typo, the handling is the same as the case below
		fallthrough
	case "grpcToGrpcTest", "grpcToGrpcWithoutVerbTest":
		response, err = s.grpcTestHandler(in.GetData().Value)
	case "retrieve_request_object":
		response, err = s.retrieveRequestObject(ctx)
	}

	if err != nil {
		msg := "Error: " + err.Error()
		response, _ = json.Marshal(msg)
	}

	respBody := &anypb.Any{Value: response}

	return &commonv1pb.InvokeResponse{Data: respBody, ContentType: "application/json"}, nil
}

// ListTopicSubscriptions Dapr will call this method to get the list of topics the app wants to subscribe to. In this example, we are telling Dapr
// To subscribe to a topic named TopicA
func (s *server) ListTopicSubscriptions(ctx context.Context, in *emptypb.Empty) (*pb.ListTopicSubscriptionsResponse, error) {
	return &pb.ListTopicSubscriptionsResponse{
		Subscriptions: []*pb.TopicSubscription{
			{
				Topic: "TopicA",
			},
		},
	}, nil
}

// ListInputBindings Dapr will call this method to get the list of bindings the app will get invoked by. In this example, we are telling Dapr
// To invoke our app with a binding named storage
func (s *server) ListInputBindings(ctx context.Context, in *emptypb.Empty) (*pb.ListInputBindingsResponse, error) {
	return &pb.ListInputBindingsResponse{
		Bindings: []string{"storage"},
	}, nil
}

// OnBindingEvent This method gets invoked every time a new event is fired from a registered binding.
// The message carries the binding name, a payload and optional metadata
func (s *server) OnBindingEvent(ctx context.Context, in *pb.BindingEventRequest) (*pb.BindingEventResponse, error) {
	fmt.Println("Invoked from binding")
	return &pb.BindingEventResponse{}, nil
}

// OnTopicEvent This method is fired whenever a message has been published to a topic that has been subscribed.
// Dapr sends published messages in a CloudEvents 1.0 envelope.
func (s *server) OnTopicEvent(ctx context.Context, in *pb.TopicEventRequest) (*pb.TopicEventResponse, error) {
	fmt.Println("Topic message arrived")
	return &pb.TopicEventResponse{}, nil
}
