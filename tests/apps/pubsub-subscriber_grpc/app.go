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
	"net/url"
	"os"
	"os/signal"
	"strings"
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
	appPort   = "3000"
	pubsubA   = "pubsub-a-topic-grpc"
	pubsubB   = "pubsub-b-topic-grpc"
	pubsubC   = "pubsub-c-topic-grpc"
	pubsubRaw = "pubsub-raw-topic-grpc"
)

var (
	// using sets to make the test idempotent on multiple delivery of same message.
	receivedMessagesA   sets.String
	receivedMessagesB   sets.String
	receivedMessagesC   sets.String
	receivedMessagesRaw sets.String

	// boolean variable to respond with empty json message if set.
	respondWithEmptyJSON bool
	// boolean variable to respond with error if set.
	respondWithError bool
	// boolean variable to respond with retry if set.
	respondWithRetry bool
	// boolean variable to respond with invalid status if set.
	respondWithInvalidStatus bool
	lock                     sync.Mutex
)

type receivedMessagesResponse struct {
	ReceivedByTopicA   []string `json:"pubsub-a-topic"`
	ReceivedByTopicB   []string `json:"pubsub-b-topic"`
	ReceivedByTopicC   []string `json:"pubsub-c-topic"`
	ReceivedByTopicRaw []string `json:"pubsub-raw-topic"`
}

// server is our user app.
type server struct{}

func main() {
	log.Printf("Initializing grpc")

	/* #nosec */
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", appPort))
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

func initializeSets() {
	receivedMessagesA = sets.NewString()
	receivedMessagesB = sets.NewString()
	receivedMessagesC = sets.NewString()
	receivedMessagesRaw = sets.NewString()
}

// This method gets invoked when a remote service has called the app through Dapr
// The payload carries a Method to identify the method, a set of metadata properties and an optional payload.
func (s *server) OnInvoke(ctx context.Context, in *commonv1pb.InvokeRequest) (*commonv1pb.InvokeResponse, error) {
	reqID := "s-" + uuid.New().String()
	if in.HttpExtension != nil && in.HttpExtension.Querystring != "" {
		qs, err := url.ParseQuery(in.HttpExtension.Querystring)
		if err == nil && qs.Has("reqid") {
			reqID = qs.Get("reqid")
		}
	}

	log.Printf("(%s) Got invoked method %s", reqID, in.Method)

	lock.Lock()
	defer lock.Unlock()

	respBody := &anypb.Any{}
	switch in.Method {
	case "getMessages":
		respBody.Value = s.getMessages(reqID)
	case "initialize":
		initializeSets()
	case "set-respond-error":
		s.setRespondWithError()
	case "set-respond-retry":
		s.setRespondWithRetry()
	case "set-respond-empty-json":
		s.setRespondWithEmptyJSON()
	case "set-respond-invalid-status":
		s.setRespondWithInvalidStatus()
	}

	return &commonv1pb.InvokeResponse{Data: respBody, ContentType: "application/json"}, nil
}

func (s *server) getMessages(reqID string) []byte {
	resp := receivedMessagesResponse{
		ReceivedByTopicA:   receivedMessagesA.List(),
		ReceivedByTopicB:   receivedMessagesB.List(),
		ReceivedByTopicC:   receivedMessagesC.List(),
		ReceivedByTopicRaw: receivedMessagesRaw.List(),
	}

	rawResp, _ := json.Marshal(resp)
	log.Printf("(%s) getMessages response: %s", reqID, string(rawResp))
	return rawResp
}

func (s *server) setRespondWithError() {
	log.Println("setRespondWithError called")
	respondWithError = true
}

func (s *server) setRespondWithRetry() {
	log.Println("setRespondWithRetry called")
	respondWithRetry = true
}

func (s *server) setRespondWithEmptyJSON() {
	log.Println("setRespondWithEmptyJSON called")
	respondWithEmptyJSON = true
}

func (s *server) setRespondWithInvalidStatus() {
	log.Println("setRespondWithInvalidStatus called")
	respondWithInvalidStatus = true
}

// Dapr will call this method to get the list of topics the app wants to subscribe to. In this example, we are telling Dapr
// to subscribe to a topic named TopicA.
func (s *server) ListTopicSubscriptions(ctx context.Context, in *emptypb.Empty) (*runtimev1pb.ListTopicSubscriptionsResponse, error) {
	log.Println("List Topic Subscription called")
	return &runtimev1pb.ListTopicSubscriptionsResponse{
		Subscriptions: []*runtimev1pb.TopicSubscription{
			{
				PubsubName: "messagebus",
				Topic:      pubsubA,
			},
			{
				PubsubName: "messagebus",
				Topic:      pubsubB,
			},
			// pubsub-c-topic-grpc is loaded from the YAML/CRD
			// tests/config/app_topic_subscription_pubsub_grpc.yaml.
			{
				PubsubName: "messagebus",
				Topic:      pubsubRaw,
				Metadata: map[string]string{
					"rawPayload": "true",
				},
			},
		},
	}, nil
}

// This method is fired whenever a message has been published to a topic that has been subscribed.
// Dapr sends published messages in a CloudEvents 1.0 envelope.
func (s *server) OnTopicEvent(ctx context.Context, in *runtimev1pb.TopicEventRequest) (*runtimev1pb.TopicEventResponse, error) {
	lock.Lock()
	defer lock.Unlock()

	reqID := uuid.New().String()
	log.Printf("(%s) Message arrived - Topic: %s, Message: %s", reqID, in.Topic, string(in.Data))

	if respondWithRetry {
		log.Printf("(%s) Responding with RETRY", reqID)
		return &runtimev1pb.TopicEventResponse{
			Status: runtimev1pb.TopicEventResponse_RETRY, //nolint:nosnakecase
		}, nil
	} else if respondWithError {
		log.Printf("(%s) Responding with ERROR", reqID)
		// do not store received messages, respond with error
		return nil, errors.New("error response")
	} else if respondWithInvalidStatus {
		log.Printf("(%s) Responding with INVALID", reqID)
		// do not store received messages, respond with success but an invalid status
		return &runtimev1pb.TopicEventResponse{
			Status: 4,
		}, nil
	}

	if in.Data == nil {
		log.Printf("(%s) Responding with DROP. in.Data is nil", reqID)
		// Return success with DROP status to drop message
		return &runtimev1pb.TopicEventResponse{
			Status: runtimev1pb.TopicEventResponse_DROP, //nolint:nosnakecase
		}, nil
	}

	var msg string
	err := json.Unmarshal(in.Data, &msg)
	if err != nil {
		log.Printf("(%s) Responding with DROP. Error while unmarshaling JSON data: %v", reqID, err)
		// Return success with DROP status to drop message
		return &runtimev1pb.TopicEventResponse{
			Status: runtimev1pb.TopicEventResponse_DROP, //nolint:nosnakecase
		}, err
	}

	// Raw data does not have content-type, so it is handled as-is.
	// Because the publisher encodes to JSON before publishing, we need to decode here.
	if strings.HasPrefix(in.Topic, pubsubRaw) {
		var actualMsg string
		err = json.Unmarshal([]byte(msg), &actualMsg)
		if err != nil {
			log.Printf("(%s) Error extracing JSON from raw event: %v", reqID, err)
		} else {
			msg = actualMsg
		}
	}

	if strings.HasPrefix(in.Topic, pubsubA) && !receivedMessagesA.Has(msg) {
		receivedMessagesA.Insert(msg)
	} else if strings.HasPrefix(in.Topic, pubsubB) && !receivedMessagesB.Has(msg) {
		receivedMessagesB.Insert(msg)
	} else if strings.HasPrefix(in.Topic, pubsubC) && !receivedMessagesC.Has(msg) {
		receivedMessagesC.Insert(msg)
	} else if strings.HasPrefix(in.Topic, pubsubRaw) && !receivedMessagesRaw.Has(msg) {
		receivedMessagesRaw.Insert(msg)
	} else {
		log.Printf("(%s) Received duplicate message: %s - %s", reqID, in.Topic, msg)
	}

	if respondWithEmptyJSON {
		log.Printf("(%s) Responding with {}", reqID)
		return &runtimev1pb.TopicEventResponse{}, nil
	}

	log.Printf("(%s) Responding with SUCCESS", reqID)
	return &runtimev1pb.TopicEventResponse{
		Status: runtimev1pb.TopicEventResponse_SUCCESS, //nolint:nosnakecase
	}, nil
}

// Dapr will call this method to get the list of bindings the app will get invoked by. In this example, we are telling Dapr
// To invoke our app with a binding named storage.
func (s *server) ListInputBindings(ctx context.Context, in *emptypb.Empty) (*runtimev1pb.ListInputBindingsResponse, error) {
	log.Println("List Input Bindings called")
	return &runtimev1pb.ListInputBindingsResponse{}, nil
}

// This method gets invoked every time a new event is fired from a registered binding. The message carries the binding name, a payload and optional metadata.
func (s *server) OnBindingEvent(ctx context.Context, in *runtimev1pb.BindingEventRequest) (*runtimev1pb.BindingEventResponse, error) {
	log.Printf("Invoked from binding: %s", in.Name)
	return &runtimev1pb.BindingEventResponse{}, nil
}
