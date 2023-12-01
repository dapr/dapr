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
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
	"k8s.io/apimachinery/pkg/util/sets"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
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
	routedMessagesA sets.Set[string]
	routedMessagesB sets.Set[string]
	routedMessagesC sets.Set[string]
	routedMessagesD sets.Set[string]
	routedMessagesE sets.Set[string]
	routedMessagesF sets.Set[string]
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

	lock.Lock()
	initializeSets()
	lock.Unlock()

	/* #nosec */
	s := grpc.NewServer()
	runtimev1pb.RegisterAppCallbackServer(s, &server{})

	log.Println("Client starting...")

	// Stop the gRPC server when we get a termination signal
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGINT) //nolint:staticcheck
	go func() {
		// Wait for cancelation signal
		<-stopCh
		log.Println("Shutdown signal received")
		s.GracefulStop()
	}()

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
	log.Println("App shut down")
}

// initialize all the sets for a clean test.
func initializeSets() {
	// initialize all the sets.
	routedMessagesA = sets.New[string]()
	routedMessagesB = sets.New[string]()
	routedMessagesC = sets.New[string]()
	routedMessagesD = sets.New[string]()
	routedMessagesE = sets.New[string]()
	routedMessagesF = sets.New[string]()
}

// This method gets invoked when a remote service has called the app through Dapr.
// The payload carries a Method to identify the method, a set of metadata properties and an optional payload.
func (s *server) OnInvoke(ctx context.Context, in *commonv1pb.InvokeRequest) (*commonv1pb.InvokeResponse, error) {
	reqID := "s-" + uuid.New().String()
	if len(in.GetHttpExtension().GetQuerystring()) > 0 {
		qs, err := url.ParseQuery(in.GetHttpExtension().GetQuerystring())
		if err == nil && qs.Has("reqid") {
			reqID = qs.Get("reqid")
		}
	}

	log.Printf("(%s) Got invoked method %s", reqID, in.GetMethod())

	lock.Lock()
	defer lock.Unlock()

	respBody := &anypb.Any{}
	switch in.GetMethod() {
	case "getMessages":
		respBody.Value = s.getMessages(reqID)
	case "initialize":
		initializeSets()
	}

	return &commonv1pb.InvokeResponse{Data: respBody, ContentType: "application/json"}, nil
}

func (s *server) getMessages(reqID string) []byte {
	resp := routedMessagesResponse{
		RouteA: sets.List(routedMessagesA),
		RouteB: sets.List(routedMessagesB),
		RouteC: sets.List(routedMessagesC),
		RouteD: sets.List(routedMessagesD),
		RouteE: sets.List(routedMessagesE),
		RouteF: sets.List(routedMessagesF),
	}

	rawResp, _ := json.Marshal(resp)
	log.Printf("(%s) getMessages response: %s", reqID, string(rawResp))
	return rawResp
}

// Dapr will call this method to get the list of topics the app wants to subscribe to. In this example, we are telling Dapr.
// To subscribe to a topic named TopicA.
func (s *server) ListTopicSubscriptions(ctx context.Context, in *emptypb.Empty) (*runtimev1pb.ListTopicSubscriptionsResponse, error) {
	log.Println("List Topic Subscription called")
	return &runtimev1pb.ListTopicSubscriptionsResponse{
		Subscriptions: []*runtimev1pb.TopicSubscription{
			{
				PubsubName: pubsubName,
				Topic:      pubsubTopic,
				Routes: &runtimev1pb.TopicRoutes{
					Rules: []*runtimev1pb.TopicRule{
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
func (s *server) OnTopicEvent(ctx context.Context, in *runtimev1pb.TopicEventRequest) (*runtimev1pb.TopicEventResponse, error) {
	lock.Lock()
	defer lock.Unlock()

	reqID := uuid.New().String()
	log.Printf("(%s) Message arrived - Topic: %s, Message: %s, Path: %s", reqID, in.GetTopic(), string(in.GetData()), in.GetPath())

	var set *sets.Set[string]
	switch in.GetPath() {
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
		log.Printf("(%s) Responding with DROP. in.GetPath() not found", reqID)
		// Return success with DROP status to drop message.
		return &runtimev1pb.TopicEventResponse{
			Status: runtimev1pb.TopicEventResponse_DROP, //nolint:nosnakecase
		}, nil
	}

	msg := string(in.GetData())

	set.Insert(msg)

	log.Printf("(%s) Responding with SUCCESS", reqID)
	return &runtimev1pb.TopicEventResponse{
		Status: runtimev1pb.TopicEventResponse_SUCCESS, //nolint:nosnakecase
	}, nil
}

// Dapr will call this method to get the list of bindings the app will get invoked by. In this example, we are telling Dapr.
// To invoke our app with a binding named storage.
func (s *server) ListInputBindings(ctx context.Context, in *emptypb.Empty) (*runtimev1pb.ListInputBindingsResponse, error) {
	log.Println("List Input Bindings called")
	return &runtimev1pb.ListInputBindingsResponse{}, nil
}

// This method gets invoked every time a new event is fired from a registered binding. The message carries the binding name, a payload and optional metadata.
func (s *server) OnBindingEvent(ctx context.Context, in *runtimev1pb.BindingEventRequest) (*runtimev1pb.BindingEventResponse, error) {
	log.Printf("Invoked from binding: %s", in.GetName())
	return &runtimev1pb.BindingEventResponse{}, nil
}
