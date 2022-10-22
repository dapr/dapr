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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gorilla/mux"
	"google.golang.org/protobuf/types/known/anypb"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	appPort                          = 3000
	daprPort                         = 3500
	DaprTestTopicEnvVar              = "DAPR_TEST_TOPIC_NAME"
	DaprTestGRPCTopicEnvVar          = "DAPR_TEST_GRPC_TOPIC_NAME"
	DaprTestInputBindingServiceEnVar = "DAPR_TEST_INPUT_BINDING_SVC"
)

var (
	daprClient      runtimev1pb.DaprClient
	topicName       = "test-topic"
	topicNameGrpc   = "test-topic-grpc"
	inputbindingSvc = "bindinginputgrpc"
)

func init() {
	if envTopicName := os.Getenv(DaprTestTopicEnvVar); len(envTopicName) != 0 {
		topicName = envTopicName
	}

	if envGrpcTopic := os.Getenv(DaprTestGRPCTopicEnvVar); len(envGrpcTopic) != 0 {
		topicNameGrpc = envGrpcTopic
	}

	if envinputBinding := os.Getenv(DaprTestInputBindingServiceEnVar); len(envinputBinding) != 0 {
		inputbindingSvc = envinputBinding
	}
}

type testCommandRequest struct {
	Messages []struct {
		Data      string `json:"data,omitempty"`
		Operation string `json:"operation"`
	} `json:"messages,omitempty"`
}

type indexHandlerResponse struct {
	Message string `json:"message,omitempty"`
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(indexHandlerResponse{Message: "OK"})
}

//nolint:gosec
func testHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Entered testHandler")
	var requestBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&requestBody)
	if err != nil {
		log.Printf("error parsing request body: %s", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	url := fmt.Sprintf("http://localhost:%d/v1.0/bindings/%s", daprPort, topicName)

	for _, message := range requestBody.Messages {
		body, err := json.Marshal(&message)
		if err != nil {
			log.Printf("error encoding message: %s", err)

			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("error: " + err.Error()))
			return
		}

		/* #nosec */
		resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
		resp.Body.Close()

		if err != nil {
			log.Printf("error sending request to output binding: %s", err)

			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("error: " + err.Error()))
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}

func sendGRPC(w http.ResponseWriter, r *http.Request) {
	log.Printf("Entered sendGRPC")
	var requestBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&requestBody)
	if err != nil {
		log.Printf("error parsing request body: %s", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	for _, message := range requestBody.Messages {
		//nolint:gosec
		body, _ := json.Marshal(&message)

		log.Printf("Sending message: %s", body)
		req := runtimev1pb.InvokeBindingRequest{
			Name:      topicNameGrpc,
			Data:      body,
			Operation: "create",
		}

		_, err = daprClient.InvokeBinding(context.Background(), &req)
		if err != nil {
			log.Printf("Error sending request to GRPC output binding: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("error: " + err.Error()))
			return
		}
	}
}

func getReceivedTopicsGRPC(w http.ResponseWriter, r *http.Request) {
	log.Printf("Entered getReceivedTopicsGRPC")

	req := runtimev1pb.InvokeServiceRequest{
		Id: inputbindingSvc,
		Message: &commonv1pb.InvokeRequest{
			Method: "GetReceivedTopics",
			Data:   &anypb.Any{},
			HttpExtension: &commonv1pb.HTTPExtension{
				Verb: commonv1pb.HTTPExtension_POST, //nolint:nosnakecase
			},
		},
	}

	resp, err := daprClient.InvokeService(context.Background(), &req)
	if err != nil {
		log.Printf("Could not get received messages: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Write(resp.Data.Value)
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/tests/send", testHandler).Methods("POST")
	router.HandleFunc("/tests/sendGRPC", sendGRPC).Methods("POST")
	router.HandleFunc("/tests/get_received_topics_grpc", getReceivedTopicsGRPC).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	daprClient = utils.GetGRPCClient(50001)

	log.Printf("Hello Dapr - listening on http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
