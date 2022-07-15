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
	"sync"

	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	pb "github.com/dapr/dapr/pkg/proto/runtime/v1"

	"google.golang.org/grpc"
)

const (
	appPort = "3000"
)

// server is our user app.
type server struct{}

type messageBuffer struct {
	lock            *sync.RWMutex
	successMessages []string
	// errorOnce is used to make sure that message is failed only once.
	errorOnce     bool
	failedMessage string
}

func (m *messageBuffer) add(message string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.successMessages = append(m.successMessages, message)
}

func (m *messageBuffer) getAllSuccessful() []string {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.successMessages
}

func (m *messageBuffer) getFailed() string {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.failedMessage
}

func (m *messageBuffer) fail(failedMessage string) bool {
	m.lock.Lock()
	defer m.lock.Unlock()
	// fail only for the first time. return false all other times.
	if !m.errorOnce {
		m.failedMessage = failedMessage
		m.errorOnce = true
		return m.errorOnce
	}
	return false
}

var messages = messageBuffer{
	lock:            &sync.RWMutex{},
	successMessages: []string{},
}

type receivedMessagesResponse struct {
	ReceivedMessages []string `json:"received_messages,omitempty"`
	Message          string   `json:"message,omitempty"`
	FailedMessage    string   `json:"failed_message,omitempty"`
}

func main() {
	log.Printf("Initializing grpc")

	/* #nosec */
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", appPort))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	/* #nosec */
	s := grpc.NewServer()
	pb.RegisterAppCallbackServer(s, &server{})

	log.Println("Client starting...")

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

func (s *server) OnInvoke(ctx context.Context, in *commonv1pb.InvokeRequest) (*commonv1pb.InvokeResponse, error) {
	fmt.Printf("Got invoked method %s and data: %s\n", in.Method, string(in.GetData().Value))

	switch in.Method {
	case "GetReceivedTopics":
		return s.GetReceivedTopics(ctx, in)
	}

	return &commonv1pb.InvokeResponse{}, nil
}

func (s *server) GetReceivedTopics(ctx context.Context, in *commonv1pb.InvokeRequest) (*commonv1pb.InvokeResponse, error) {
	failedMessage := messages.getFailed()
	log.Printf("failed message %s", failedMessage)
	resp := receivedMessagesResponse{
		ReceivedMessages: messages.getAllSuccessful(),
		FailedMessage:    failedMessage,
	}
	rawResp, err := json.Marshal(resp)
	if err != nil {
		log.Printf("Could not encode response: %s", err.Error())
		return &commonv1pb.InvokeResponse{}, err
	}
	data := anypb.Any{
		Value: rawResp,
	}
	return &commonv1pb.InvokeResponse{
		Data: &data,
	}, nil
}

// Dapr will call this method to get the list of topics the app wants to subscribe to.
func (s *server) ListTopicSubscriptions(ctx context.Context, in *emptypb.Empty) (*pb.ListTopicSubscriptionsResponse, error) {
	log.Println("List Topic Subscription called")
	return &pb.ListTopicSubscriptionsResponse{
		Subscriptions: []*pb.TopicSubscription{},
	}, nil
}

// This method is fired whenever a message has been published to a topic that has been subscribed. Dapr sends published messages in a CloudEvents 1.0 envelope.
func (s *server) OnTopicEvent(ctx context.Context, in *pb.TopicEventRequest) (*pb.TopicEventResponse, error) {
	log.Printf("Message arrived - Topic: %s, Message: %s\n", in.Topic, string(in.Data))

	var message string
	err := json.Unmarshal(in.Data, &message)
	log.Printf("Got message: %s", message)
	if err != nil {
		log.Printf("error parsing test-topic input binding payload: %s", err)
		return &pb.TopicEventResponse{}, nil
	}
	if fail := messages.fail(message); fail {
		// simulate failure. fail only for the first time.
		log.Print("failing message")
		return &pb.TopicEventResponse{}, nil
	}
	messages.add(message)

	return &pb.TopicEventResponse{
		Status: pb.TopicEventResponse_SUCCESS,
	}, nil
}

func (s *server) ListInputBindings(ctx context.Context, in *emptypb.Empty) (*pb.ListInputBindingsResponse, error) {
	log.Println("List Input Bindings called")
	return &pb.ListInputBindingsResponse{
		Bindings: []string{
			"test-topic-grpc",
		},
	}, nil
}

// This method gets invoked every time a new event is fired from a registered binding. The message carries the binding name, a payload and optional metadata.
func (s *server) OnBindingEvent(ctx context.Context, in *pb.BindingEventRequest) (*pb.BindingEventResponse, error) {
	fmt.Printf("Invoked from binding: %s - %s\n", in.Name, string(in.Data))
	return &pb.BindingEventResponse{}, nil
}
