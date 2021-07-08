// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
	"k8s.io/apimachinery/pkg/util/sets"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

const (
	appPort     = 3000
	pubsubName  = "messagebus"
	pubsubTopic = "pubsub-routing-grpc"

	pathA = "myevent.A"
	pathB = "myevent.B"
	pathC = "myevent.C"
	pathD = "myevent.D"
	pathE = "myevent.E"
	pathF = "myevent.F"
)

type routedMessagesResponse struct {
	RouteA []string `json:"route-a"`
	RouteB []string `json:"route-b"`
	RouteC []string `json:"route-c"`
	RouteD []string `json:"route-d"`
	RouteE []string `json:"route-e"`
	RouteF []string `json:"route-f"`
}

var (
	// using sets to make the test idempotent on multiple delivery of same message.
	routedMessagesA sets.String
	routedMessagesB sets.String
	routedMessagesC sets.String
	routedMessagesD sets.String
	routedMessagesE sets.String
	routedMessagesF sets.String
	lock            sync.Mutex
)

// server is our user app.
type server struct{}

func main() {
	log.Printf("Initializing grpc")

	/* #nosec */
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", appPort))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	initializeSets()

	/* #nosec */
	s := grpc.NewServer()
	pb.RegisterAppCallbackServer(s, &server{})

	log.Println("Client starting...")

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

// initialize all the sets for a clean test.
func initializeSets() {
	lock.Lock()
	defer lock.Unlock()

	// initialize all the sets.
	routedMessagesA = sets.NewString()
	routedMessagesB = sets.NewString()
	routedMessagesC = sets.NewString()
	routedMessagesD = sets.NewString()
	routedMessagesE = sets.NewString()
	routedMessagesF = sets.NewString()
}

// This method gets invoked when a remote service has called the app through Dapr.
// The payload carries a Method to identify the method, a set of metadata properties and an optional payload.
func (s *server) OnInvoke(ctx context.Context, in *commonv1pb.InvokeRequest) (*commonv1pb.InvokeResponse, error) {
	log.Printf("Got invoked method %s\n", in.Method)

	respBody := &anypb.Any{}
	switch in.Method {
	case "getMessages":
		respBody.Value = s.getRoutedMessages()
	case "initialize":
		initializeSets()
	}

	return &commonv1pb.InvokeResponse{Data: respBody, ContentType: "application/json"}, nil
}

func (s *server) getRoutedMessages() []byte {
	resp := routedMessagesResponse{
		RouteA: routedMessagesA.List(),
		RouteB: routedMessagesB.List(),
		RouteC: routedMessagesC.List(),
		RouteD: routedMessagesD.List(),
		RouteE: routedMessagesE.List(),
		RouteF: routedMessagesF.List(),
	}

	rawResp, _ := json.Marshal(resp)
	return rawResp
}

// Dapr will call this method to get the list of topics the app wants to subscribe to. In this example, we are telling Dapr.
// To subscribe to a topic named TopicA.
func (s *server) ListTopicSubscriptions(ctx context.Context, in *emptypb.Empty) (*pb.ListTopicSubscriptionsResponse, error) {
	log.Println("List Topic Subscription called")
	return &pb.ListTopicSubscriptionsResponse{
		Subscriptions: []*pb.TopicSubscription{
			{
				PubsubName: pubsubName,
				Topic:      pubsubTopic,
				Routes: &pb.TopicRoutes{
					Rules: []*pb.TopicRule{
						{
							Match: `event.type == "myevent.C"`,
							Path:  pathC,
						},
						{
							Match: `event.type == "myevent.B"`,
							Path:  pathB,
						},
					},
					Default: pathA,
				},
			},
		},
	}, nil
}

// This method is fired whenever a message has been published to a topic that has been subscribed. Dapr sends published messages in a CloudEvents 1.0 envelope.
func (s *server) OnTopicEvent(ctx context.Context, in *pb.TopicEventRequest) (*pb.TopicEventResponse, error) {
	log.Printf("Message arrived - Topic: %s, Message: %s, Path: %s\n", in.Topic, string(in.Data), in.Path)

	var set *sets.String
	switch in.Path {
	case pathA:
		set = &routedMessagesA
	case pathB:
		set = &routedMessagesB
	case pathC:
		set = &routedMessagesC
	case pathD:
		set = &routedMessagesD
	case pathE:
		set = &routedMessagesE
	case pathF:
		set = &routedMessagesF
	default:
		// Return success with DROP status to drop message.
		return &pb.TopicEventResponse{
			Status: pb.TopicEventResponse_DROP,
		}, nil
	}

	msg := string(in.Data)

	lock.Lock()
	defer lock.Unlock()
	set.Insert(msg)

	return &pb.TopicEventResponse{
		Status: pb.TopicEventResponse_SUCCESS,
	}, nil
}

// Dapr will call this method to get the list of bindings the app will get invoked by. In this example, we are telling Dapr.
// To invoke our app with a binding named storage.
func (s *server) ListInputBindings(ctx context.Context, in *emptypb.Empty) (*pb.ListInputBindingsResponse, error) {
	log.Println("List Input Bindings called")
	return &pb.ListInputBindingsResponse{}, nil
}

// This method gets invoked every time a new event is fired from a registered binding. The message carries the binding name, a payload and optional metadata.
func (s *server) OnBindingEvent(ctx context.Context, in *pb.BindingEventRequest) (*pb.BindingEventResponse, error) {
	fmt.Printf("Invoked from binding: %s\n", in.Name)
	return &pb.BindingEventResponse{}, nil
}
