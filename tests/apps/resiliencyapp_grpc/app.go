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
	"errors"
	"fmt"
	"log"
	"net"
	"time"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"

	"google.golang.org/grpc"
	pb "google.golang.org/grpc/examples/helloworld/helloworld"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	appPort = "3000"
)

// This is our app, which registers various gRPC calls.
type server struct {
	callTracking map[string][]CallRecord
	pb.UnimplementedGreeterServer
}

type CallRecord struct {
	Count    int
	TimeSeen time.Time
}

type FailureMessage struct {
	ID              string         `json:"id"`
	MaxFailureCount *int           `json:"maxFailureCount,omitempty"`
	Timeout         *time.Duration `json:"timeout,omitempty"`
}

// SayHello implements helloworld.GreeterServer
// Instead of defining our own proto and generating the client/server, we're going to force our
// use case in the sample code.
func (s *server) SayHello(ctx context.Context, in *pb.HelloRequest) (*pb.HelloReply, error) {
	var message FailureMessage
	err := json.Unmarshal([]byte(in.Name), &message)
	if err != nil {
		return nil, errors.New("failed to decode message")
	}

	callCount := 0
	if records, ok := s.callTracking[message.ID]; ok {
		callCount = records[len(records)-1].Count + 1
	}

	log.Printf("Proxy has seen %s %d times.", message.ID, callCount)

	s.callTracking[message.ID] = append(s.callTracking[message.ID], CallRecord{Count: callCount, TimeSeen: time.Now()})

	if message.MaxFailureCount != nil && callCount < *message.MaxFailureCount {
		if message.Timeout != nil {
			// This request can still succeed if the resiliency policy timeout is longer than this sleep.
			log.Println("Sleeping.")
			time.Sleep(*message.Timeout)
		} else {
			log.Println("Forcing failure.")
			return nil, errors.New("forced failure")
		}
	}
	return &pb.HelloReply{Message: "Hello"}, nil
}

// gRPC server definitions.
func (s *server) OnInvoke(ctx context.Context, in *commonv1pb.InvokeRequest) (*commonv1pb.InvokeResponse, error) {
	log.Printf("Got invoked method %s and data: %s\n", in.Method, string(in.GetData().Value))

	resp := &commonv1pb.InvokeResponse{}

	if in.Method == "GetCallCount" {
		log.Println("Getting call counts")
		for key, val := range s.callTracking {
			log.Printf("\t%s - Called %d times.\n", key, len(val))
		}
		b, err := json.Marshal(s.callTracking)

		if err != nil {
			resp.Data = &anypb.Any{}
		} else {
			resp.Data = &anypb.Any{
				Value: b,
			}
		}
	} else if in.Method == "grpcInvoke" {
		var message FailureMessage
		err := json.Unmarshal(in.Data.Value, &message)
		if err != nil {
			return nil, errors.New("failed to decode message")
		}

		callCount := 0
		if records, ok := s.callTracking[message.ID]; ok {
			callCount = records[len(records)-1].Count + 1
		}

		log.Printf("Seen %s %d times.", message.ID, callCount)

		s.callTracking[message.ID] = append(s.callTracking[message.ID], CallRecord{Count: callCount, TimeSeen: time.Now()})

		if message.MaxFailureCount != nil && callCount < *message.MaxFailureCount {
			if message.Timeout != nil {
				// This request can still succeed if the resiliency policy timeout is longer than this sleep.
				log.Println("Sleeping.")
				time.Sleep(*message.Timeout)
			} else {
				log.Println("Forcing failure.")
				return nil, errors.New("forced failure")
			}
		}
		resp.Data = &anypb.Any{}
	}

	return resp, nil
}

// Dapr will call this method to get the list of topics the app wants to subscribe to.
func (s *server) ListTopicSubscriptions(ctx context.Context, in *emptypb.Empty) (*runtimev1pb.ListTopicSubscriptionsResponse, error) {
	log.Println("List Topic Subscription called")
	return &runtimev1pb.ListTopicSubscriptionsResponse{
		Subscriptions: []*runtimev1pb.TopicSubscription{
			{
				PubsubName: "dapr-resiliency-pubsub",
				Topic:      "resiliency-topic-grpc",
			},
		},
	}, nil
}

// This method is fired whenever a message has been published to a topic that has been subscribed. Dapr sends published messages in a CloudEvents 1.0 envelope.
func (s *server) OnTopicEvent(ctx context.Context, in *runtimev1pb.TopicEventRequest) (*runtimev1pb.TopicEventResponse, error) {
	log.Printf("Message arrived - Topic: %s, Message: %s\n", in.Topic, string(in.Data))

	var message FailureMessage
	err := json.Unmarshal(in.Data, &message)
	if err != nil {
		return nil, errors.New("failed to decode message")
	}

	callCount := 0
	if records, ok := s.callTracking[message.ID]; ok {
		callCount = records[len(records)-1].Count + 1
	}

	log.Printf("Seen %s %d times.", message.ID, callCount)

	s.callTracking[message.ID] = append(s.callTracking[message.ID], CallRecord{Count: callCount, TimeSeen: time.Now()})

	if message.MaxFailureCount != nil && callCount < *message.MaxFailureCount {
		if message.Timeout != nil {
			// This request can still succeed if the resiliency policy timeout is longer than this sleep.
			log.Println("Sleeping.")
			time.Sleep(*message.Timeout)
		} else {
			log.Println("Forcing failure.")
			return nil, errors.New("forced failure")
		}
	}

	return &runtimev1pb.TopicEventResponse{
		Status: runtimev1pb.TopicEventResponse_SUCCESS,
	}, nil
}

func (s *server) ListInputBindings(ctx context.Context, in *emptypb.Empty) (*runtimev1pb.ListInputBindingsResponse, error) {
	log.Println("List Input Bindings called")
	return &runtimev1pb.ListInputBindingsResponse{
		Bindings: []string{
			"dapr-resiliency-binding-grpc",
		},
	}, nil
}

// This method gets invoked every time a new event is fired from a registered binding. The message carries the binding name, a payload and optional metadata.
func (s *server) OnBindingEvent(ctx context.Context, in *runtimev1pb.BindingEventRequest) (*runtimev1pb.BindingEventResponse, error) {
	log.Printf("Invoked from binding: %s - %s\n", in.Name, string(in.Data))

	var message FailureMessage
	err := json.Unmarshal(in.Data, &message)
	if err != nil {
		return nil, errors.New("failed to decode message")
	}

	callCount := 0
	if records, ok := s.callTracking[message.ID]; ok {
		callCount = records[len(records)-1].Count + 1
	}

	log.Printf("Seen %s %d times.", message.ID, callCount)

	s.callTracking[message.ID] = append(s.callTracking[message.ID], CallRecord{Count: callCount, TimeSeen: time.Now()})

	if message.MaxFailureCount != nil && callCount < *message.MaxFailureCount {
		if message.Timeout != nil {
			// This request can still succeed if the resiliency policy timeout is longer than this sleep.
			log.Println("Sleeping.")
			time.Sleep(*message.Timeout)
		} else {
			log.Println("Forcing failure.")
			return nil, errors.New("forced failure")
		}
	}

	return &runtimev1pb.BindingEventResponse{}, nil
}

// Init.
func main() {
	log.Printf("Initializing grpc")

	/* #nosec */
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", appPort))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	/* #nosec */
	server := &server{
		callTracking: map[string][]CallRecord{},
	}
	s := grpc.NewServer()
	runtimev1pb.RegisterAppCallbackServer(s, server)
	pb.RegisterGreeterServer(s, server)

	log.Println("Client starting...")

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
