// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"

	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
	"k8s.io/apimachinery/pkg/util/sets"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	pb "github.com/dapr/dapr/pkg/proto/runtime/v1"

	"google.golang.org/grpc"
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

	initializeSets()

	/* #nosec */
	s := grpc.NewServer()
	pb.RegisterAppCallbackServer(s, &server{})

	log.Println("Client starting...")

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

func initializeSets() {
	lock.Lock()
	defer lock.Unlock()

	receivedMessagesA = sets.NewString()
	receivedMessagesB = sets.NewString()
	receivedMessagesC = sets.NewString()
	receivedMessagesRaw = sets.NewString()
}

// This method gets invoked when a remote service has called the app through Dapr
// The payload carries a Method to identify the method, a set of metadata properties and an optional payload.
func (s *server) OnInvoke(ctx context.Context, in *commonv1pb.InvokeRequest) (*commonv1pb.InvokeResponse, error) {
	log.Printf("Got invoked method %s\n", in.Method)

	respBody := &anypb.Any{}
	switch in.Method {
	case "getMessages":
		respBody.Value = s.getReceivedMessages()
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

func (s *server) getReceivedMessages() []byte {
	lock.Lock()
	defer lock.Unlock()

	resp := receivedMessagesResponse{
		ReceivedByTopicA:   receivedMessagesA.List(),
		ReceivedByTopicB:   receivedMessagesB.List(),
		ReceivedByTopicC:   receivedMessagesC.List(),
		ReceivedByTopicRaw: receivedMessagesRaw.List(),
	}

	rawResp, _ := json.Marshal(resp)
	return rawResp
}

func (s *server) setRespondWithError() {
	log.Println("setRespondWithError called")
	lock.Lock()
	defer lock.Unlock()
	respondWithError = true
}

func (s *server) setRespondWithRetry() {
	log.Println("setRespondWithRetry called")
	lock.Lock()
	defer lock.Unlock()
	respondWithRetry = true
}

func (s *server) setRespondWithEmptyJSON() {
	log.Println("setRespondWithEmptyJSON called")
	lock.Lock()
	defer lock.Unlock()
	respondWithEmptyJSON = true
}

func (s *server) setRespondWithInvalidStatus() {
	log.Println("setRespondWithInvalidStatus called")
	lock.Lock()
	defer lock.Unlock()
	respondWithInvalidStatus = true
}

// Dapr will call this method to get the list of topics the app wants to subscribe to. In this example, we are telling Dapr
// To subscribe to a topic named TopicA.
func (s *server) ListTopicSubscriptions(ctx context.Context, in *emptypb.Empty) (*pb.ListTopicSubscriptionsResponse, error) {
	log.Println("List Topic Subscription called")
	return &pb.ListTopicSubscriptionsResponse{
		Subscriptions: []*pb.TopicSubscription{
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

// This method is fired whenever a message has been published to a topic that has been subscribed. Dapr sends published messages in a CloudEvents 1.0 envelope.
func (s *server) OnTopicEvent(ctx context.Context, in *pb.TopicEventRequest) (*pb.TopicEventResponse, error) {
	log.Printf("Message arrived - Topic: %s, Message: %s\n", in.Topic, string(in.Data))

	if respondWithRetry {
		return &pb.TopicEventResponse{
			Status: pb.TopicEventResponse_RETRY,
		}, nil
	} else if respondWithError {
		// do not store received messages, respond with error
		return nil, errors.New("error response")
	} else if respondWithInvalidStatus {
		// do not store received messages, respond with success but an invalid status
		return &pb.TopicEventResponse{
			Status: 4,
		}, nil
	}

	if in.Data == nil {
		// Return success with DROP status to drop message
		return &pb.TopicEventResponse{
			Status: pb.TopicEventResponse_DROP,
		}, nil
	}

	var msg string
	err := json.Unmarshal(in.Data, &msg)
	if err != nil {
		// Return success with DROP status to drop message
		return &pb.TopicEventResponse{
			Status: pb.TopicEventResponse_DROP,
		}, nil
	}

	// Raw data does not have content-type, so it is handled as-is.
	// Because the publisher encodes to JSON before publishing, we need to decode here.
	if strings.HasPrefix(in.Topic, pubsubRaw) {
		var actualMsg string
		err = json.Unmarshal([]byte(msg), &actualMsg)
		if err != nil {
			log.Printf("Error extracing JSON from raw event: %v", err)
		} else {
			msg = actualMsg
		}
	}

	lock.Lock()
	defer lock.Unlock()
	if strings.HasPrefix(in.Topic, pubsubA) && !receivedMessagesA.Has(msg) {
		receivedMessagesA.Insert(msg)
	} else if strings.HasPrefix(in.Topic, pubsubB) && !receivedMessagesB.Has(msg) {
		receivedMessagesB.Insert(msg)
	} else if strings.HasPrefix(in.Topic, pubsubC) && !receivedMessagesC.Has(msg) {
		receivedMessagesC.Insert(msg)
	} else if strings.HasPrefix(in.Topic, pubsubRaw) && !receivedMessagesRaw.Has(msg) {
		receivedMessagesRaw.Insert(msg)
	} else {
		log.Printf("Received duplicate message: %s - %s", in.Topic, msg)
	}

	if respondWithEmptyJSON {
		return &pb.TopicEventResponse{}, nil
	}
	return &pb.TopicEventResponse{
		Status: pb.TopicEventResponse_SUCCESS,
	}, nil
}

// Dapr will call this method to get the list of bindings the app will get invoked by. In this example, we are telling Dapr
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
